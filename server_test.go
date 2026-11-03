package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// dialWS opens a raw WebSocket to the test server (client mode: masked frames).
func dialWS(t *testing.T, serverURL string) *wsConn {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	k := make([]byte, 16)
	rand.Read(k)
	key := base64.StdEncoding.EncodeToString(k)
	req := "GET /ws HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nOrigin: http://" + u.Host + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 101 {
		t.Fatalf("handshake status %d", resp.StatusCode)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") == "" {
		t.Fatal("missing Sec-WebSocket-Accept")
	}
	return &wsConn{conn: conn, br: br, client: true}
}

type serverMsg struct {
	Type  string       `json:"type"`
	Msg   string       `json:"msg"`
	Code  string       `json:"code"`
	Seat  int          `json:"seat"`
	Token string       `json:"token"`
	You   int          `json:"you"`
	Seats []SeatPublic `json:"seats"`
	Game  View         `json:"game"`
	raw   string
}

func (c *wsConn) send(t *testing.T, s string) {
	t.Helper()
	if err := c.writeFrame(opText, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

func (c *wsConn) next(t *testing.T) serverMsg {
	t.Helper()
	raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m serverMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad json %q: %v", raw, err)
	}
	m.raw = string(raw)
	return m
}

// until reads messages until pred is true.
func (c *wsConn) until(t *testing.T, pred func(serverMsg) bool) serverMsg {
	t.Helper()
	for {
		m := c.next(t)
		if pred(m) {
			return m
		}
	}
}

func TestWebsocketHandshakeRejectsPlainHTTP(t *testing.T) {
	srv := httptest.NewServer(NewHub(DefaultRules(), 0).routes("0"))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain GET /ws: status %d, want 400", resp.StatusCode)
	}
	// wrong origin is refused
	req, _ := http.NewRequest("GET", srv.URL+"/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Origin", "http://evil.example")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin upgrade: status %d, want 403", resp2.StatusCode)
	}
}

func TestStaticPages(t *testing.T) {
	srv := httptest.NewServer(NewHub(DefaultRules(), 0).routes("0"))
	defer srv.Close()
	for _, path := range []string{"/", "/play", "/table", "/api/info"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("GET %s: status %d", path, resp.StatusCode)
		}
	}
}

// One human (who just draws and discards) plus three bots play a hand to the end,
// with a spectator watching. Checks seating, reconnects, turn enforcement,
// and that spectators never see anybody's cards.
func TestWebsocketHand(t *testing.T) {
	readTimeout = 10 * time.Second
	hub := NewHub(DefaultRules(), 0)
	srv := httptest.NewServer(hub.routes("0"))
	defer srv.Close()

	spec := dialWS(t, srv.URL)
	defer spec.Close()
	first := spec.next(t)
	if first.Type != "state" || first.You != -1 || first.Game.Phase != PhaseIdle {
		t.Fatalf("spectator should start with an idle state: %+v", first)
	}

	p := dialWS(t, srv.URL)
	defer p.Close()
	p.next(t) // initial state
	p.send(t, `{"type":"draw"}`)
	if m := p.until(t, func(m serverMsg) bool { return m.Type == "error" }); !strings.Contains(m.Msg, "seat") {
		t.Errorf("acting before sitting should be refused, got %q", m.Msg)
	}
	p.send(t, `{"type":"join","name":"Tester","seat":0}`)
	joined := p.until(t, func(m serverMsg) bool { return m.Type == "joined" })
	if joined.Seat != 0 || len(joined.Token) < 16 {
		t.Fatalf("bad join reply: %+v", joined)
	}

	// someone else can't take the same seat, and can't start with empty seats
	other := dialWS(t, srv.URL)
	defer other.Close()
	other.next(t)
	other.send(t, `{"type":"join","name":"Thief","seat":0}`)
	if m := other.until(t, func(m serverMsg) bool { return m.Type == "error" }); !strings.Contains(m.Msg, "taken") {
		t.Errorf("expected a seat-taken error, got %q", m.Msg)
	}
	p.send(t, `{"type":"start"}`)
	if m := p.until(t, func(m serverMsg) bool { return m.Type == "error" }); !strings.Contains(m.Msg, "four seats") {
		t.Errorf("expected a not-enough-players error, got %q", m.Msg)
	}

	// reconnect with the token from another socket: the seat moves over
	p2 := dialWS(t, srv.URL)
	defer p2.Close()
	p2.next(t)
	p2.send(t, `{"type":"rejoin","token":"`+joined.Token+`"}`)
	if m := p2.until(t, func(m serverMsg) bool { return m.Type == "joined" }); m.Seat != 0 {
		t.Fatalf("rejoin gave seat %d", m.Seat)
	}
	p2.send(t, `{"type":"rejoin","token":"nope"}`)
	if m := p2.until(t, func(m serverMsg) bool { return m.Type == "error" }); m.Code != "badtoken" {
		t.Errorf("bad token should report code badtoken, got %+v", m)
	}
	p = p2 // continue playing on the new socket

	p.send(t, `{"type":"fillbots"}`)
	p.send(t, `{"type":"start"}`)

	deadline := time.Now().Add(30 * time.Second)
	var last serverMsg
	sawSpectatorPlaying := false
	for time.Now().Before(deadline) {
		m := p.next(t)
		if m.Type != "state" {
			continue
		}
		last = m
		g := m.Game
		if g.Phase == PhaseHandOver || g.Phase == PhaseGameOver {
			break
		}
		if g.Phase == PhasePlaying && g.Turn == 0 {
			if len(g.Hand) == 0 {
				t.Fatal("player received no hand")
			}
			switch g.TurnStep {
			case StepDraw:
				if !g.CanDraw {
					t.Fatal("canDraw should be true when it is my draw step")
				}
				p.send(t, `{"type":"draw"}`)
			case StepPlay:
				// out-of-turn action by a spectator must be refused
				spec.send(t, `{"type":"draw"}`)
				id, _ := json.Marshal(g.Hand[0].ID)
				p.send(t, `{"type":"discard","card":`+string(id)+`}`)
			}
		}
	}
	if last.Game.Phase != PhaseHandOver && last.Game.Phase != PhaseGameOver {
		t.Fatalf("hand did not finish; last phase %s, turn %d", last.Game.Phase, last.Game.Turn)
	}
	if last.Game.Result == nil {
		t.Fatal("finished hand has no result")
	}

	// the spectator saw the same hand finish and was never sent a hand
	for {
		m := spec.next(t)
		if m.Type != "state" {
			continue
		}
		if strings.Contains(m.raw, `"hand":`) {
			t.Fatal("spectator state leaked a hand")
		}
		if m.Game.Phase == PhasePlaying {
			sawSpectatorPlaying = true
		}
		if m.Game.Phase == PhaseHandOver || m.Game.Phase == PhaseGameOver {
			break
		}
	}
	if !sawSpectatorPlaying {
		t.Error("spectator never saw the hand in progress")
	}

	// the next hand can be dealt from the handover screen
	if last.Game.Phase == PhaseHandOver {
		before := last.Game.HandNo
		p.send(t, `{"type":"next"}`)
		m := p.until(t, func(m serverMsg) bool { return m.Type == "state" && m.Game.Phase == PhasePlaying })
		if m.Game.HandNo != before+1 || m.Game.Dealer != last.Game.NextDealer {
			t.Errorf("next hand: no %d dealer %d, want no %d dealer %d", m.Game.HandNo, m.Game.Dealer, before+1, last.Game.NextDealer)
		}
	}
}
