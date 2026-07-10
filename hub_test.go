package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestServer starts the ws handler on an httptest server and returns a
// dialer helper that connects with an optional explicit room code.
func newTestServer(t *testing.T) (dial func(room string) *websocket.Conn, url string, closeAll func()) {
	t.Helper()
	hub := NewHub()
	srv := httptest.NewServer(wsHandler(hub))

	var conns []*websocket.Conn
	dial = func(room string) *websocket.Conn {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
		if room != "" {
			u += "?room=" + room
		}
		c, _, err := websocket.DefaultDialer.Dial(u, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		conns = append(conns, c)
		return c
	}
	closeAll = func() {
		for _, c := range conns {
			c.Close()
		}
		srv.Close()
	}
	return dial, srv.URL, closeAll
}

// readMsg reads one JSON message with a deadline, failing the test on timeout.
func readMsg(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// readUntil reads messages until one of the given type arrives (or times out).
func readUntil(t *testing.T, c *websocket.Conn, typ string) Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsg(t, c)
		if m.Type == typ {
			return m
		}
	}
	t.Fatalf("did not receive %q in time", typ)
	return Message{}
}

// handshake drains the two messages every client receives on connect (welcome
// then peers) and returns them. After this the connection's read buffer is
// quiet unless another peer acts.
func handshake(t *testing.T, c *websocket.Conn) (welcome Message, peers Message) {
	t.Helper()
	welcome = readMsg(t, c)
	if welcome.Type != "welcome" {
		t.Fatalf("expected welcome first, got %q", welcome.Type)
	}
	peers = readMsg(t, c)
	if peers.Type != "peers" {
		t.Fatalf("expected peers after welcome, got %q", peers.Type)
	}
	return welcome, peers
}

// expectSilence asserts the connection receives nothing within a short window.
func expectSilence(t *testing.T, c *websocket.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatalf("expected no message but one was delivered")
	}
}

func TestWelcomeCarriesRoom(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	c := dial("myroom")
	w := readMsg(t, c)
	if w.Type != "welcome" {
		t.Fatalf("expected welcome first, got %q", w.Type)
	}
	if w.From == "" || w.Name == "" {
		t.Fatalf("welcome missing identity: %+v", w)
	}
	if w.Room != "code-myroom" {
		t.Fatalf("expected room code-myroom, got %q", w.Room)
	}
}

func TestPeersSeeEachOtherInSameRoom(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	a := dial("shared")
	readUntil(t, a, "welcome")

	b := dial("shared")
	// a should be told that b joined.
	joined := readUntil(t, a, "peer-joined")
	if joined.Name == "" {
		t.Fatalf("peer-joined missing name")
	}
	// b's initial peer list should contain a.
	peers := readUntil(t, b, "peers")
	if len(peers.Peers) != 1 {
		t.Fatalf("expected 1 existing peer for b, got %d", len(peers.Peers))
	}
}

func TestRoomsAreIsolated(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	a := dial("alpha")
	handshake(t, a)

	b := dial("beta")
	// b's peer list must be empty — a is in a different room.
	_, peers := handshake(t, b)
	if len(peers.Peers) != 0 {
		t.Fatalf("expected b to see no peers across rooms, got %d", len(peers.Peers))
	}

	// a must NOT receive a peer-joined for b.
	expectSilence(t, a)
}

// TestCodelessConnectionsAreIsolated verifies the opt-in guarantee: two clients
// that connect without a room code land in separate throwaway rooms and never
// discover each other, even though they share the test server's loopback IP.
func TestCodelessConnectionsAreIsolated(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	a := dial("")
	aw, aPeers := handshake(t, a)
	if !strings.HasPrefix(aw.Room, "solo-") {
		t.Fatalf("expected a solo room, got %q", aw.Room)
	}
	if len(aPeers.Peers) != 0 {
		t.Fatalf("expected a to start alone, got %d peers", len(aPeers.Peers))
	}

	b := dial("")
	_, bPeers := handshake(t, b)
	if len(bPeers.Peers) != 0 {
		t.Fatalf("expected b to see no one, got %d peers", len(bPeers.Peers))
	}
	// a must not be told about b.
	expectSilence(t, a)
}

func TestCrossRoomRoutingIsDropped(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	a := dial("one")
	handshake(t, a)
	b := dial("two")
	bw, _ := handshake(t, b)

	// a tries to route an offer to b, who is in another room.
	if err := a.WriteJSON(Message{Type: "offer", To: bw.From, SDP: "x"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// b must not receive it.
	expectSilence(t, b)
}

func TestSameRoomRoutingDelivers(t *testing.T) {
	dial, _, closeAll := newTestServer(t)
	defer closeAll()

	a := dial("rt")
	aw := readUntil(t, a, "welcome")
	b := dial("rt")
	bw := readUntil(t, b, "welcome")
	readUntil(t, a, "peer-joined")

	if err := a.WriteJSON(Message{Type: "offer", To: bw.From, SDP: "hello"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readUntil(t, b, "offer")
	if got.From != aw.From {
		t.Fatalf("offer from mismatch: got %q want %q", got.From, aw.From)
	}
	if got.SDP != "hello" {
		t.Fatalf("offer sdp mismatch: got %q", got.SDP)
	}
}

// TestRoomCapEnforced verifies the hard 8-peer product limit: the ninth
// connection to a room is rejected before the WebSocket upgrade.
func TestRoomCapEnforced(t *testing.T) {
	dial, srvURL, closeAll := newTestServer(t)
	defer closeAll()

	for i := 0; i < maxRoomSize; i++ {
		c := dial("packed")
		readUntil(t, c, "welcome")
	}

	// The next dial must be refused with 503 Service Unavailable.
	u := "ws" + strings.TrimPrefix(srvURL, "http") + "/ws?room=packed"
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		t.Fatalf("expected the %dth peer to be rejected", maxRoomSize+1)
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		got := "no response"
		if resp != nil {
			got = fmt.Sprintf("%d", resp.StatusCode)
		}
		t.Fatalf("expected 503 for a full room, got %s", got)
	}
}

func TestHealthEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("health = %d %q", rec.Code, rec.Body.String())
	}
}
