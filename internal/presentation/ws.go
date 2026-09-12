package presentation

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/httpx"
)

const (
	// A window that has not answered a ping in this long is gone.
	pongWait   = 60 * time.Second
	pingPeriod = pongWait * 9 / 10
	writeWait  = 10 * time.Second

	maxMessageSize = 1 << 20
	sendBuffer     = 32
)

type Handler struct {
	repo *Repo
	hub  *Hub
	// CheckOrigin is permissive because the clients are desktop windows, not
	// browsers: there is no ambient cookie for a cross-site request to abuse,
	// and every connection still has to present a valid bearer token.
	upgrader websocket.Upgrader
}

func NewHandler(repo *Repo, hub *Hub) *Handler {
	return &Handler{
		repo: repo,
		hub:  hub,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/state", h.get)
	r.Put("/state", h.put)
	r.Get("/ws", h.serveWS)
	return r
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	state, err := h.repo.Get(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// put is the fallback for a client that cannot hold a socket open. The
// websocket path is the one that runs during a service.
func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	var state State
	if err := httpx.Decode(r, &state); err != nil {
		httpx.WriteError(w, err)
		return
	}
	saved, err := h.repo.Upsert(r.Context(), userID, state)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.hub.BroadcastState(userID, nil, saved)
	httpx.JSON(w, http.StatusOK, saved)
}

func (h *Handler) serveWS(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserID(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.ErrUnauthorized)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote a response.
		return
	}

	c := &client{userID: userID, send: make(chan []byte, sendBuffer)}
	h.hub.join(c)

	go h.writePump(conn, c)
	h.readPump(conn, c)
}

// readPump owns the connection's lifetime: when it returns, the client leaves
// the room and the socket closes.
func (h *Handler) readPump(conn *websocket.Conn, c *client) {
	defer func() {
		h.hub.leave(c)
		_ = conn.Close()
	}()

	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Send the stored state immediately, so a window that opens mid-service
	// shows the current slide instead of waiting for the next change.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	current, err := h.repo.Get(ctx, c.userID)
	cancel()
	if err == nil {
		if payload, err := json.Marshal(envelope{Type: "state", State: &current}); err == nil {
			select {
			case c.send <- payload:
			default:
			}
		}
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("presentation: read: %v", err)
			}
			return
		}

		var msg envelope
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "state" || msg.State == nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		saved, err := h.repo.Upsert(ctx, c.userID, *msg.State)
		cancel()
		if err != nil {
			log.Printf("presentation: persist state: %v", err)
			// Fan the change out regardless. A projector that keeps up with the
			// operator matters more than a row that failed to save.
			saved = *msg.State
		}
		h.hub.BroadcastState(c.userID, c, saved)
	}
}

func (h *Handler) writePump(conn *websocket.Conn, c *client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ensure the id type is referenced even when the build tags change.
var _ = uuid.Nil
