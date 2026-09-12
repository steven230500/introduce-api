package presentation

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// newTestClient registers a client in the hub and returns it.
func newTestClient(t *testing.T, h *Hub, userID uuid.UUID) *client {
	t.Helper()
	c := &client{userID: userID, send: make(chan []byte, 4)}
	h.join(c)
	return c
}

func TestBroadcastReachesTheOtherWindows(t *testing.T) {
	h := NewHub()
	operator := uuid.New()

	control := newTestClient(t, h, operator)
	projector := newTestClient(t, h, operator)
	stage := newTestClient(t, h, operator)

	h.Broadcast(operator, control, []byte("slide-2"))

	for name, c := range map[string]*client{"projector": projector, "stage": stage} {
		select {
		case got := <-c.send:
			if string(got) != "slide-2" {
				t.Fatalf("%s received %q", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s never received the change", name)
		}
	}

	// The sender must not be echoed its own change, or the control window would
	// fight the operator for the cursor position.
	select {
	case got := <-control.send:
		t.Fatalf("the sender was echoed %q", got)
	default:
	}
}

func TestBroadcastStaysInsideTheRoom(t *testing.T) {
	h := NewHub()
	mine := uuid.New()
	theirs := uuid.New()

	myProjector := newTestClient(t, h, mine)
	theirProjector := newTestClient(t, h, theirs)

	h.Broadcast(mine, nil, []byte("mi slide"))

	select {
	case <-myProjector.send:
	case <-time.After(time.Second):
		t.Fatal("my own projector missed the change")
	}

	// One church's service must never appear on another's screen.
	select {
	case got := <-theirProjector.send:
		t.Fatalf("a different operator received %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLeaveStopsDelivery(t *testing.T) {
	h := NewHub()
	operator := uuid.New()
	projector := newTestClient(t, h, operator)

	h.leave(projector)
	h.Broadcast(operator, nil, []byte("x"))

	// leave closes the channel, so a receive returns immediately and not ok.
	if _, ok := <-projector.send; ok {
		t.Fatal("a window that left still received a change")
	}
	if h.Connections() != 0 {
		t.Fatalf("hub still counts %d connections", h.Connections())
	}
}

func TestSlowWindowIsDroppedNotWaitedOn(t *testing.T) {
	h := NewHub()
	operator := uuid.New()

	// A window that has stopped reading: its buffer is already full.
	stalled := &client{userID: operator, send: make(chan []byte, 1)}
	stalled.send <- []byte("backlog")
	h.join(stalled)

	healthy := newTestClient(t, h, operator)

	done := make(chan struct{})
	go func() {
		h.Broadcast(operator, nil, []byte("slide-3"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a stalled window blocked the broadcast")
	}

	select {
	case got := <-healthy.send:
		if string(got) != "slide-3" {
			t.Fatalf("healthy window received %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the healthy window was starved by the stalled one")
	}

	if h.Connections() != 1 {
		t.Fatalf("expected the stalled window to be dropped, %d remain", h.Connections())
	}
}

func TestConnectionsCountsEveryRoom(t *testing.T) {
	h := NewHub()
	a, b := uuid.New(), uuid.New()
	newTestClient(t, h, a)
	newTestClient(t, h, a)
	newTestClient(t, h, b)

	if got := h.Connections(); got != 3 {
		t.Fatalf("Connections() = %d, want 3", got)
	}
}
