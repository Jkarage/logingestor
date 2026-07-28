package logapp

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jkarage/logingestor/business/domain/logbus"
)

// writeWait bounds how long a single WebSocket write may block on a slow or
// dead subscriber before its connection is dropped. Without it a stalled
// client's TCP buffer fills and the write blocks forever (the server's
// WriteTimeout no longer applies after the upgrade hijacks the connection).
const writeWait = 5 * time.Second

// sendBuffer is the per-subscriber queue of pending broadcast frames. When it
// fills, new frames are dropped for that subscriber: live tail is best-effort
// and must never apply backpressure to the ingest path.
const sendBuffer = 32

// BroadcastLogs converts persisted logs to their API representation and fans
// them out to WebSocket subscribers, grouped by project. It is the shared
// live-tail entry point for both app-log and infra-log ingestion paths.
func (h *Hub) BroadcastLogs(logs []logbus.Log) {
	byProject := make(map[uuid.UUID][]LogEntry)
	for _, l := range logs {
		byProject[l.ProjectID] = append(byProject[l.ProjectID], toAppLogEntry(l))
	}
	for pid, entries := range byProject {
		h.broadcast(pid, entries)
	}
}

// connState owns the outbound side of one WebSocket connection. Frames are
// queued on send and written by a dedicated writer goroutine — the only
// goroutine that writes to the connection, which also satisfies
// gorilla/websocket's single-writer requirement. done is closed exactly once
// when the subscriber goes away, stopping the writer.
type connState struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

func newConnState(conn *websocket.Conn) *connState {
	cs := &connState{
		conn: conn,
		send: make(chan []byte, sendBuffer),
		done: make(chan struct{}),
	}
	go cs.writer()
	return cs
}

func (cs *connState) writer() {
	defer cs.conn.Close()
	for {
		select {
		case <-cs.done:
			return
		case data := <-cs.send:
			_ = cs.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := cs.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				// Closing the conn (deferred) unblocks the handler's read
				// loop, which unsubscribes this connection from the hub.
				cs.stop()
				return
			}
		}
	}
}

func (cs *connState) stop() {
	cs.once.Do(func() { close(cs.done) })
}

// enqueue hands a frame to the writer without ever blocking. Frames for a
// stopped or saturated subscriber are dropped.
func (cs *connState) enqueue(data []byte) {
	select {
	case <-cs.done:
	case cs.send <- data:
	default:
	}
}

// Hub maintains per-project WebSocket subscriber sets and broadcasts
// new log entries to them as they are ingested.
type Hub struct {
	mu    sync.RWMutex
	rooms map[uuid.UUID]map[*websocket.Conn]*connState
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		rooms: make(map[uuid.UUID]map[*websocket.Conn]*connState),
	}
}

func (h *Hub) subscribe(projectID uuid.UUID, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[projectID] == nil {
		h.rooms[projectID] = make(map[*websocket.Conn]*connState)
	}
	h.rooms[projectID][conn] = newConnState(conn)
}

func (h *Hub) unsubscribe(projectID uuid.UUID, conn *websocket.Conn) {
	h.mu.Lock()
	var cs *connState
	if room, ok := h.rooms[projectID]; ok {
		cs = room[conn]
		delete(room, conn)
		if len(room) == 0 {
			delete(h.rooms, projectID)
		}
	}
	h.mu.Unlock()

	if cs != nil {
		cs.stop()
	}
}

// broadcast sends all entries as a JSON array to every WebSocket connection
// subscribed to projectID. Sending the whole batch as one array frame is more
// efficient than one frame per entry and also easier for the frontend to
// handle. Frames are queued to each subscriber's writer goroutine, so this
// never blocks on a subscriber's network and is safe to call from the ingest
// request path.
func (h *Hub) broadcast(projectID uuid.UUID, entries []LogEntry) {
	h.mu.RLock()
	states := make([]*connState, 0, len(h.rooms[projectID]))
	for _, cs := range h.rooms[projectID] {
		states = append(states, cs)
	}
	h.mu.RUnlock()

	if len(states) == 0 {
		return
	}

	// Marshal the whole batch once, then send the same bytes to every subscriber.
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}

	for _, cs := range states {
		cs.enqueue(data)
	}
}
