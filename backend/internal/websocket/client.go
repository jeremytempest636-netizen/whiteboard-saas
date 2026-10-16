package websocket

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = pongWait * 9 / 10
	sendBuffer = 256

	// A client may send this many messages per second on average, with bursts.
	// Cursor moves are throttled client side to ~30/s, drawing is batched.
	rateLimitPerSec = 150
	rateLimitBurst  = 300
	// A connection that keeps exceeding the limit is closed.
	maxRateViolations = 100
)

// Client is one WebSocket connection. Exactly two goroutines touch the socket:
// readPump (the only reader) and writePump (the only writer). Everything else
// talks to the client through the send channel, which only the room goroutine
// writes to and closes.
type Client struct {
	id    string
	who   Identity
	color string

	conn *websocket.Conn
	room *Room
	send chan []byte
	log  *zap.Logger

	// Written by the room goroutine before it closes send; read by writePump
	// after it observes the close.
	closeInfo atomic.Value

	// Touched only by readPump.
	limiter    bucket
	violations int
	badOps     int
}

func newClient(conn *websocket.Conn, who Identity, log *zap.Logger) *Client {
	return &Client{
		id:      newClientID(),
		who:     who,
		conn:    conn,
		send:    make(chan []byte, sendBuffer),
		limiter: newBucket(rateLimitPerSec, rateLimitBurst),
		log:     log,
	}
}

func (c *Client) peer() Peer { return Peer{ID: c.id, Name: c.who.Name, Color: c.color} }

// readPump reads messages until the connection drops. It never writes to the
// socket; replies go through the room or the send channel.
func (c *Client) readPump() {
	defer func() {
		c.room.unregister(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				c.log.Debug("read error", zap.String("client", c.id), zap.Error(err))
			}
			return
		}
		// Any inbound traffic proves the peer is alive, not only pongs.
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		if !c.limiter.allow() {
			c.violations++
			if c.violations > maxRateViolations {
				c.log.Warn("closing abusive client", zap.String("client", c.id), zap.String("user", c.who.UserID))
				return
			}
			continue
		}

		var m clientMessage
		if err := json.Unmarshal(data, &m); err != nil {
			c.reject("bad_json", "message is not valid JSON")
			if c.tooManyBadMessages() {
				return
			}
			continue
		}

		switch m.Type {
		case TypePing:
			if !c.room.dispatch(inbound{from: c, reply: frame(simpleMessage{Type: TypePong})}) {
				return
			}
		case TypeOp, TypeClear, TypeCursor:
			if !c.room.dispatch(inbound{from: c, msg: m}) {
				return
			}
		default:
			c.reject("unknown_type", "unknown message type")
			if c.tooManyBadMessages() {
				return
			}
		}
	}
}

func (c *Client) tooManyBadMessages() bool {
	c.badOps++
	return c.badOps > 50
}

// writePump is the only writer. It exits when the room closes the send
// channel, after telling the peer why.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				code, reason := c.room.closeReason(c)
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// trySend queues a frame without blocking. If the buffer is full the client is
// too slow to keep up; the caller (room goroutine only) must then drop it.
// Droppable frames (cursors, pongs) are skipped early to protect the buffer
// for edits.
func (c *Client) trySend(msg []byte, droppable bool) bool {
	if droppable && len(c.send) > cap(c.send)*3/4 {
		return true
	}
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// reject tells the client its message was refused. The read goroutine must not
// touch the send channel (the room goroutine may close it at any moment), so
// the reply is routed through the room.
func (c *Client) reject(code, message string) {
	c.room.dispatch(inbound{from: c, reply: frame(errorMessage{Type: TypeError, Code: code, Message: message})})
}

// bucket is a small token bucket used for per-connection rate limiting.
type bucket struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(perSecond, burst int) bucket {
	return bucket{rate: float64(perSecond), burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (b *bucket) allow() bool {
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// newClientID returns a random 12 hex character connection id.
func newClientID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
