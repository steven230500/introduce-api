package presentation_test

import (
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// These exercise a running server. They skip unless INTRODUCE_TEST_API and
// INTRODUCE_TEST_TOKEN point at one, so `go test ./...` stays hermetic.
func endpoint(t *testing.T) (string, string) {
	t.Helper()
	api := os.Getenv("INTRODUCE_TEST_API")
	token := os.Getenv("INTRODUCE_TEST_TOKEN")
	if api == "" || token == "" {
		t.Skip("set INTRODUCE_TEST_API and INTRODUCE_TEST_TOKEN to run this")
	}
	return api, token
}

func dial(t *testing.T, api, token string) *websocket.Conn {
	t.Helper()
	u, err := url.Parse(api)
	if err != nil {
		t.Fatalf("parse api url: %v", err)
	}
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.Path = "/presentation/ws"
	// The token rides as a query parameter: a desktop window opens the socket
	// through a client that cannot always set headers on the upgrade.
	u.RawQuery = "access_token=" + token

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (http %d)", u.Redacted(), err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type message struct {
	Type  string `json:"type"`
	State struct {
		CurrentItemIndex  int  `json:"current_item_index"`
		CurrentSlideIndex int  `json:"current_slide_index"`
		IsLive            bool `json:"is_live"`
		BlankScreen       bool `json:"blank_screen"`
	} `json:"state"`
}

func read(t *testing.T, conn *websocket.Conn) message {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg message
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func TestProjectorReceivesTheCurrentStateOnConnect(t *testing.T) {
	api, token := endpoint(t)
	projector := dial(t, api, token)

	// A window that opens mid-service must be told where the operator is,
	// rather than sitting black until the next slide change.
	msg := read(t, projector)
	if msg.Type != "state" {
		t.Fatalf("first message was %q, want state", msg.Type)
	}
}

func TestProjectorFollowsTheControlWindow(t *testing.T) {
	api, token := endpoint(t)

	control := dial(t, api, token)
	read(t, control) // the initial state

	projector := dial(t, api, token)
	read(t, projector) // the initial state

	if err := control.WriteJSON(map[string]any{
		"type": "state",
		"state": map[string]any{
			"current_item_index":  2,
			"current_slide_index": 5,
			"is_live":             true,
			"blank_screen":        false,
		},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := read(t, projector)
	if got.State.CurrentItemIndex != 2 || got.State.CurrentSlideIndex != 5 {
		t.Fatalf("projector is on item %d slide %d, want 2 and 5",
			got.State.CurrentItemIndex, got.State.CurrentSlideIndex)
	}
	if !got.State.IsLive {
		t.Fatal("the projector did not learn that the feed went live")
	}
}

func TestStateChangeIsPersisted(t *testing.T) {
	api, token := endpoint(t)

	control := dial(t, api, token)
	read(t, control)

	if err := control.WriteJSON(map[string]any{
		"type": "state",
		"state": map[string]any{
			"current_item_index":  7,
			"current_slide_index": 1,
			"is_live":             false,
			"blank_screen":        true,
		},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Give the write a moment to land, then open a fresh window: what it is
	// handed on connect comes from the database, not from memory.
	time.Sleep(500 * time.Millisecond)
	fresh := dial(t, api, token)
	got := read(t, fresh)

	if got.State.CurrentItemIndex != 7 {
		t.Fatalf("a new window was handed item %d, want 7", got.State.CurrentItemIndex)
	}
	if !got.State.BlankScreen {
		t.Fatal("the blank screen did not survive a reconnect")
	}
}
