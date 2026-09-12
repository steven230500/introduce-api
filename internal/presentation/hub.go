package presentation

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/google/uuid"
)

// Hub fans out state changes to every window belonging to one operator.
//
// It replaces the hosted realtime service the app used to depend on. Rooms are
// keyed by user, because a projector window only ever follows the control
// window of the person running the service.
type Hub struct {
	mu    sync.RWMutex
	rooms map[uuid.UUID]map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[uuid.UUID]map[*client]struct{})}
}

type client struct {
	userID uuid.UUID
	send   chan []byte
}

func (h *Hub) join(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room, ok := h.rooms[c.userID]
	if !ok {
		room = make(map[*client]struct{})
		h.rooms[c.userID] = room
	}
	room[c] = struct{}{}
}

func (h *Hub) leave(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room, ok := h.rooms[c.userID]
	if !ok {
		return
	}
	if _, ok := room[c]; ok {
		delete(room, c)
		close(c.send)
	}
	if len(room) == 0 {
		delete(h.rooms, c.userID)
	}
}

// Broadcast sends payload to every window of userID except sender.
//
// A slow or dead connection is dropped rather than waited on: one stalled
// window must never delay the projector.
func (h *Hub) Broadcast(userID uuid.UUID, sender *client, payload []byte) {
	h.mu.RLock()
	room := make([]*client, 0, len(h.rooms[userID]))
	for c := range h.rooms[userID] {
		if c != sender {
			room = append(room, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range room {
		select {
		case c.send <- payload:
		default:
			log.Printf("presentation: dropping a window that stopped reading")
			h.leave(c)
		}
	}
}

// BroadcastState marshals and fans out a state change.
func (h *Hub) BroadcastState(userID uuid.UUID, sender *client, state State) {
	payload, err := json.Marshal(envelope{Type: "state", State: &state})
	if err != nil {
		log.Printf("presentation: marshal state: %v", err)
		return
	}
	h.Broadcast(userID, sender, payload)
}

// Connections reports how many windows are listening, for the health endpoint.
func (h *Hub) Connections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, room := range h.rooms {
		total += len(room)
	}
	return total
}

// envelope is the single message shape on the wire, in both directions.
type envelope struct {
	Type  string `json:"type"`
	State *State `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}
