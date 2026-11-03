package main

// Queens - a four-player partnership card game.
// Go server + WebSocket clients written in JavaScript (see ./web).

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
)

//go:embed web
var webFS embed.FS

// ---------------------------------------------------------------- protocol

// Msg is anything a browser may send. Only the fields relevant to Type are used.
type Msg struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Seat  *int   `json:"seat"`
	Token string `json:"token"`
	Cards []int  `json:"cards"`
	Card  *int   `json:"card"`
	Meld  *int   `json:"meld"`
}

type SeatPublic struct {
	Seat      int    `json:"seat"`
	Name      string `json:"name"`
	Taken     bool   `json:"taken"`
	Connected bool   `json:"connected"`
	Bot       bool   `json:"bot"`
}

type outState struct {
	Type  string       `json:"type"`
	You   int          `json:"you"` // seat of this client, -1 if not seated
	Seats []SeatPublic `json:"seats"`
	Game  View         `json:"game"`
}

// ---------------------------------------------------------------- hub

type Client struct {
	ws   *wsConn
	send chan []byte
	seat int // -1 = spectator or not yet seated; guarded by Hub.mu
}

// push queues a message; a client that cannot keep up is dropped.
func (c *Client) push(b []byte) {
	select {
	case c.send <- b:
	default:
		c.ws.Close()
	}
}

func (c *Client) pushJSON(v any) {
	if b, err := json.Marshal(v); err == nil {
		c.push(b)
	}
}

func (c *Client) pushError(msg, code string) {
	c.pushJSON(map[string]string{"type": "error", "msg": msg, "code": code})
}

func (c *Client) writePump() {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b, ok := <-c.send:
			if !ok {
				c.ws.Close()
				return
			}
			if err := c.ws.WriteText(b); err != nil {
				c.ws.Close()
				return
			}
		case <-ping.C:
			if err := c.ws.WritePing(); err != nil {
				c.ws.Close()
				return
			}
		}
	}
}

type seatInfo struct {
	Taken  bool
	Name   string
	Token  string
	Bot    bool
	client *Client
}

type Hub struct {
	mu         sync.Mutex
	rules      Rules
	game       *Game
	seats      [NumSeats]seatInfo
	clients    map[*Client]struct{}
	botDelay   time.Duration
	botPending bool
}

func NewHub(rules Rules, botDelay time.Duration) *Hub {
	return &Hub{rules: rules, game: NewGame(rules), clients: map[*Client]struct{}{}, botDelay: botDelay}
}

func (h *Hub) seatsPublic() []SeatPublic {
	out := make([]SeatPublic, NumSeats)
	for i, s := range h.seats {
		out[i] = SeatPublic{Seat: i, Name: s.Name, Taken: s.Taken, Bot: s.Bot, Connected: s.Bot || s.client != nil}
	}
	return out
}

func (h *Hub) stateBytes(seat int) []byte {
	b, _ := json.Marshal(outState{Type: "state", You: seat, Seats: h.seatsPublic(), Game: h.game.View(seat)})
	return b
}

func (h *Hub) broadcastLocked() {
	cache := map[int][]byte{}
	for c := range h.clients {
		b, ok := cache[c.seat]
		if !ok {
			b = h.stateBytes(c.seat)
			cache[c.seat] = b
		}
		c.push(b)
	}
}

func (h *Hub) syncNamesLocked() {
	for i, s := range h.seats {
		if s.Taken {
			h.game.Names[i] = s.Name
		} else {
			h.game.Names[i] = seatNames[i]
		}
	}
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrade(w, r)
	if err != nil {
		return
	}
	c := &Client{ws: ws, send: make(chan []byte, 32), seat: -1}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	c.push(h.stateBytes(-1))
	h.mu.Unlock()
	go c.writePump()
	h.readPump(c)
}

func (h *Hub) readPump(c *Client) {
	defer h.unregister(c)
	for {
		raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var m Msg
		if err := json.Unmarshal(raw, &m); err != nil {
			c.pushError("could not understand that message", "")
			continue
		}
		h.handle(c, m)
	}
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	if c.seat >= 0 && h.seats[c.seat].client == c {
		h.seats[c.seat].client = nil
	}
	close(c.send)
	c.ws.Close()
	h.broadcastLocked()
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	r := []rune(strings.TrimSpace(s))
	if len(r) > 18 {
		r = r[:18]
	}
	return strings.TrimSpace(string(r))
}

// handle processes one message from one client.
func (h *Hub) handle(c *Client, m Msg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	g := h.game
	var err error
	code := ""
	switch m.Type {
	case "join":
		err = h.join(c, m)
	case "rejoin":
		if err = h.rejoin(c, m); err != nil {
			code = "badtoken"
		}
	case "leave":
		err = h.leave(c)
	case "fillbots":
		err = h.fillBots()
	case "start", "next":
		err = h.startHand()
	case "newgame":
		if g.Phase != PhaseGameOver {
			err = errors.New("the current game is not finished")
		} else {
			h.game = NewGame(h.rules)
			h.syncNamesLocked()
		}
	case "reset":
		h.reset()
	case "order":
		// Hand re-ordering is private to the player: no broadcast needed.
		if err = g.SetHandOrder(c.seat, m.Cards); err != nil {
			c.pushError(err.Error(), "")
		}
		return
	case "draw", "pickup", "meld", "add", "discard":
		if c.seat < 0 {
			err = errors.New("take a seat first")
			break
		}
		switch m.Type {
		case "draw":
			err = g.Draw(c.seat)
		case "pickup":
			err = g.Pickup(c.seat, m.Cards)
		case "meld":
			err = g.NewMeld(c.seat, m.Cards)
		case "add":
			if m.Meld == nil {
				err = errors.New("choose a meld to add to")
			} else {
				err = g.AddToMeld(c.seat, *m.Meld, m.Cards)
			}
		case "discard":
			if m.Card == nil {
				err = errors.New("choose a card to discard")
			} else {
				err = g.DiscardCard(c.seat, *m.Card)
			}
		}
	default:
		err = errors.New("unknown command")
	}
	if err != nil {
		c.pushError(err.Error(), code)
		return
	}
	h.broadcastLocked()
	h.scheduleBotLocked()
}

func (h *Hub) join(c *Client, m Msg) error {
	if c.seat >= 0 {
		return errors.New("you already have a seat")
	}
	if m.Seat == nil || *m.Seat < 0 || *m.Seat >= NumSeats {
		return errors.New("choose a seat")
	}
	s := *m.Seat
	if h.seats[s].Taken {
		return errors.New("that seat is already taken")
	}
	name := cleanName(m.Name)
	if name == "" {
		name = seatNames[s]
	}
	tok := newToken()
	h.seats[s] = seatInfo{Taken: true, Name: name, Token: tok, client: c}
	c.seat = s
	h.syncNamesLocked()
	h.game.logf("%s sat down at %s.", name, seatNames[s])
	c.pushJSON(map[string]any{"type": "joined", "seat": s, "token": tok, "name": name})
	return nil
}

func (h *Hub) rejoin(c *Client, m Msg) error {
	if m.Token == "" {
		return errors.New("unknown seat token")
	}
	for i, s := range h.seats {
		if s.Taken && !s.Bot && s.Token == m.Token {
			if old := s.client; old != nil && old != c {
				old.seat = -1
				old.ws.Close()
			}
			h.seats[i].client = c
			c.seat = i
			c.pushJSON(map[string]any{"type": "joined", "seat": i, "token": s.Token, "name": s.Name})
			return nil
		}
	}
	return errors.New("that seat is no longer yours - pick a seat to join again")
}

func (h *Hub) leave(c *Client) error {
	if c.seat < 0 {
		return nil
	}
	if h.game.Phase != PhaseIdle {
		return errors.New("you can't leave a game in progress")
	}
	h.seats[c.seat] = seatInfo{}
	c.seat = -1
	h.syncNamesLocked()
	return nil
}

func (h *Hub) fillBots() error {
	if h.game.Phase != PhaseIdle {
		return errors.New("bots can only be added before the game starts")
	}
	for i := range h.seats {
		if !h.seats[i].Taken {
			h.seats[i] = seatInfo{Taken: true, Bot: true, Name: "Bot " + seatNames[i]}
		}
	}
	h.syncNamesLocked()
	return nil
}

func (h *Hub) startHand() error {
	for _, s := range h.seats {
		if !s.Taken {
			return errors.New("all four seats must be filled before dealing")
		}
	}
	return h.game.StartHand()
}

func (h *Hub) reset() {
	h.game = NewGame(h.rules)
	h.seats = [NumSeats]seatInfo{}
	for c := range h.clients {
		c.seat = -1
	}
	h.syncNamesLocked()
}

// scheduleBotLocked arranges for a bot to take its turn after a short pause,
// so humans can follow what happened.
func (h *Hub) scheduleBotLocked() {
	g := h.game
	if h.botPending || g.Phase != PhasePlaying || !h.seats[g.Turn].Bot {
		return
	}
	h.botPending = true
	time.AfterFunc(h.botDelay, h.runBot)
}

func (h *Hub) runBot() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.botPending = false
	g := h.game
	if g.Phase != PhasePlaying || !h.seats[g.Turn].Bot {
		return
	}
	botPlay(g, g.Turn)
	h.broadcastLocked()
	h.scheduleBotLocked()
}

// ---------------------------------------------------------------- http

func lanURLs(port string) []string {
	var urls []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			urls = append(urls, "http://"+net.JoinHostPort(ipn.IP.String(), port))
		}
	}
	return urls
}

func (h *Hub) routes(port string) http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.serveWS)
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"urls": lanURLs(port), "rules": h.rules})
	})
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, sub, name) }
	}
	mux.HandleFunc("/play", page("play.html"))
	mux.HandleFunc("/table", page("table.html"))
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		mux.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	rules := DefaultRules()
	flag.IntVar(&rules.TargetScore, "target", rules.TargetScore, "score a team needs to win the game")
	flag.BoolVar(&rules.JokersWild, "jokers-wild", rules.JokersWild, "make jokers wild too, playable once the team is down (default: only 2s are wild)")
	flag.BoolVar(&rules.PickupRequiresDown, "pickup-requires-down", rules.PickupRequiresDown, "a team must already be down to take the discard pile")
	botDelay := flag.Duration("bot-delay", 1500*time.Millisecond, "pause before a bot plays its turn")
	flag.Parse()

	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("bad -addr: %v", err)
	}
	hub := NewHub(rules, *botDelay)

	fmt.Println("Queens is running.")
	fmt.Printf("  Table (spectator) : http://localhost:%s/table\n", port)
	fmt.Printf("  Players           : http://localhost:%s/play\n", port)
	for _, u := range lanURLs(port) {
		fmt.Printf("  On your network   : %s/play   (phones / tablets)\n", u)
	}
	log.Fatal(http.ListenAndServe(*addr, hub.routes(port)))
}
