package logapp

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Hub maintains per-project WebSocket subscriber sets and broadcasts
// new log entries to them as they are ingested.
type Hub struct {
	mu    sync.RWMutex
	rooms map[uuid.UUID]map[*websocket.Conn]struct{}
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		rooms: make(map[uuid.UUID]map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) subscribe(projectID uuid.UUID, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[projectID] == nil {
		h.rooms[projectID] = make(map[*websocket.Conn]struct{})
	}
	h.rooms[projectID][conn] = struct{}{}
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

// broadcast sends entries to all WebSocket connections subscribed to projectID.
func (h *Hub) broadcast(projectID uuid.UUID, entries []LogEntry) {
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.rooms[projectID]))
	for c := range h.rooms[projectID] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	if len(conns) == 0 {
		return
	}

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		for _, c := range conns {
			_ = c.WriteMessage(websocket.TextMessage, data)
		}
	}
}
