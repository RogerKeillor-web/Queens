package main

// A small, dependency-free implementation of the server side of RFC 6455
// (WebSockets): handshake, text/binary messages, fragmentation, ping/pong and
// close. It is deliberately minimal - just enough for the game protocol.
// The same code can run in "client" mode (masked frames), which the tests use.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opCont  = 0x0
	opText  = 0x1
	opBin   = 0x2
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

const (
	maxMessage   = 1 << 16
	writeTimeout = 10 * time.Second
)

// readTimeout is how long a connection may stay silent (the server pings every
// 25s and browsers answer automatically). A variable so tests can shorten it.
var readTimeout = 75 * time.Second

var errClosed = errors.New("websocket closed")

type wsConn struct {
	conn   net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	client bool // client mode: mask outgoing frames, expect unmasked incoming ones
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), token) {
				return true
			}
		}
	}
	return false
}

// sameOrigin allows requests with no Origin header (non-browser clients) and
// browser requests coming from the page this server itself served.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// upgrade performs the server handshake. On failure it has already written an
// HTTP error response.
func upgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Method != http.MethodGet ||
		!headerHasToken(r.Header, "Connection", "upgrade") ||
		!headerHasToken(r.Header, "Upgrade", "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return nil, errors.New("not a websocket upgrade request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusUpgradeRequired)
		return nil, errors.New("unsupported websocket version")
	}
	if !sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return nil, errors.New("origin not allowed")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("missing key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support hijacking", http.StatusInternalServerError)
		return nil, errors.New("no hijacker")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetWriteDeadline(time.Time{})
	return &wsConn{conn: conn, br: rw.Reader}, nil
}

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	n := len(payload)
	buf := make([]byte, 0, n+14)
	buf = append(buf, 0x80|op)
	var maskBit byte
	if c.client {
		maskBit = 0x80
	}
	switch {
	case n < 126:
		buf = append(buf, maskBit|byte(n))
	case n <= 0xFFFF:
		buf = append(buf, maskBit|126, byte(n>>8), byte(n))
	default:
		buf = append(buf, maskBit|127)
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(n))
		buf = append(buf, l[:]...)
	}
	if c.client {
		var m [4]byte
		if _, err := rand.Read(m[:]); err != nil {
			return err
		}
		buf = append(buf, m[:]...)
		for i, b := range payload {
			buf = append(buf, b^m[i%4])
		}
	} else {
		buf = append(buf, payload...)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(buf)
	return err
}

func (c *wsConn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		err = errors.New("websocket: reserved bits set")
		return
	}
	op = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	if !c.client && !masked {
		err = errors.New("websocket: client frame is not masked")
		return
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxMessage {
		err = errors.New("websocket: frame too large")
		return
	}
	if op >= 0x8 && (n > 125 || !fin) {
		err = errors.New("websocket: bad control frame")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// ReadMessage returns the next complete text or binary message. Ping, pong and
// close frames are handled internally. It returns errClosed after a close frame.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	started := false
	for {
		c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		fin, op, p, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, p); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			c.writeFrame(opClose, p)
			return nil, errClosed
		case opText, opBin:
			if started {
				return nil, errors.New("websocket: new message inside fragmented message")
			}
			started = true
			msg = p
		case opCont:
			if !started {
				return nil, errors.New("websocket: unexpected continuation frame")
			}
			msg = append(msg, p...)
			if len(msg) > maxMessage {
				return nil, errors.New("websocket: message too large")
			}
		default:
			return nil, errors.New("websocket: unknown opcode")
		}
		if fin {
			return msg, nil
		}
	}
}

func (c *wsConn) WriteText(b []byte) error { return c.writeFrame(opText, b) }
func (c *wsConn) WritePing() error         { return c.writeFrame(opPing, nil) }
func (c *wsConn) Close() error             { return c.conn.Close() }
