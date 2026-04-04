package logapp

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// connState wraps a WebSocket connection with its own write mutex.
// gorilla/websocket requires that only one concurrent goroutine writes
// to a connection at a time; this mutex enforces that invariant so
// simultaneous ingest calls don't corrupt frames.
type connState struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (cs *connState) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.conn.WriteMessage(websocket.TextMessage, data)
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
	h.rooms[projectID][conn] = &connState{conn: conn}
}

func (h *Hub) unsubscribe(projectID uuid.UUID, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if room, ok := h.rooms[projectID]; ok {
		delete(room, conn)
		if len(room) == 0 {
			delete(h.rooms, projectID)
		}
	}
}

// broadcast sends all entries as a JSON array to every WebSocket connection
// subscribed to projectID. Sending the whole batch as one array frame is more
// efficient than one frame per entry and also easier for the frontend to handle.
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
		cs.mu.Lock()
		_ = cs.conn.WriteMessage(websocket.TextMessage, data)
		cs.mu.Unlock()
	}
}
