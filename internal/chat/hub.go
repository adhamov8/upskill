package chat

import (
	"sync"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*websocket.Conn]struct{})}
}

func (h *Hub) Join(room string, c *websocket.Conn) {
	h.mu.Lock()
	if _, ok := h.rooms[room]; !ok {
		h.rooms[room] = make(map[*websocket.Conn]struct{})
	}
	h.rooms[room][c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) Leave(room string, c *websocket.Conn) {
	h.mu.Lock()
	if m, ok := h.rooms[room]; ok {
		delete(m, c)
		if len(m) == 0 {
			delete(h.rooms, room)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) Broadcast(room string, payload any) {
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.rooms[room]))
	for c := range h.rooms[room] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		_ = c.WriteJSON(payload)
	}
}
