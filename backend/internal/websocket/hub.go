package websocket

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"whiteboard/internal/pool"
)

// Errors surfaced to callers of Join.
var (
	ErrRoomFull     = errors.New("websocket: room is full")
	ErrRoomClosed   = errors.New("websocket: room closed")
	ErrShuttingDown = errors.New("websocket: server is shutting down")
)

// SnapshotStore is the persistence port used by rooms. It is implemented by
// the service layer on top of PostgreSQL.
type SnapshotStore interface {
	// Load returns the latest saved document, or (nil, nil) for a new board.
	Load(ctx context.Context, boardID string) ([]byte, error)
	// Save stores a document containing the given number of live elements.
	Save(ctx context.Context, boardID string, data []byte, elements int) error
}

// Options configure a Hub.
type Options struct {
	NodeID           string
	AllowedOrigins   []string
	SnapshotDebounce time.Duration
	SnapshotMaxWait  time.Duration
	IdleTimeout      time.Duration
	MaxRoomClients   int
}

// Identity is the authenticated person behind a connection.
type Identity struct {
	UserID string
	Name   string
}

// Hub owns every Room on this node. All access to the room map is guarded by
// one mutex; everything inside a room is owned by that room's goroutine.
type Hub struct {
	opts     Options
	log      *zap.Logger
	bus      *Bus
	store    SnapshotStore
	pool     *pool.Pool
	upgrader websocket.Upgrader
	mu       sync.RWMutex
	rooms    map[string]*Room
	closing  bool
	roomsWG  sync.WaitGroup
}

// NewHub creates a hub. Call SetBus before serving traffic.
func NewHub(opts Options, store SnapshotStore, workers *pool.Pool, log *zap.Logger) *Hub {
	h := &Hub{
		opts:  opts,
		log:   log.Named("hub"),
		store: store,
		pool:  workers,
		rooms: make(map[string]*Room),
	}
	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     originChecker(opts.AllowedOrigins),
	}
	return h
}

// SetBus attaches the Redis adapter. It is separate from NewHub because the bus
// needs the hub's Dispatch method as its message handler.
func (h *Hub) SetBus(b *Bus) { h.bus = b }

func originChecker(allowed []string) func(*http.Request) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}
	return func(r *http.Request) bool {
		if _, any := set["*"]; any {
			return true
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client
		}
		_, ok := set[origin]
		return ok
	}
}

// Dispatch routes a Redis message to the room it belongs to. It is the
// handler passed to NewBus.
func (h *Hub) Dispatch(channel, payload string) {
	boardID, ok := boardFromChannel(channel)
	if !ok {
		return
	}
	h.mu.RLock()
	room := h.rooms[boardID]
	h.mu.RUnlock()
	if room == nil {
		return
	}
	room.deliverRemote([]byte(payload))
}

// ServeWS upgrades the request and attaches the connection to the board's room.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, boardID string, who Identity) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Debug("upgrade failed", zap.Error(err))
		return
	}
	c := newClient(conn, who, h.log)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	room, err := h.join(ctx, boardID, c)
	if err != nil {
		code, reason := websocket.CloseTryAgainLater, "could not join board"
		switch {
		case errors.Is(err, ErrRoomFull):
			code, reason = websocket.ClosePolicyViolation, "board is full"
		case errors.Is(err, ErrShuttingDown):
			code, reason = websocket.CloseServiceRestart, "server restarting"
		}
		h.log.Warn("join failed", zap.String("board", boardID), zap.Error(err))
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(writeWait))
		_ = conn.Close()
		return
	}
	c.room = room

	go c.writePump()
	go c.readPump()
}

// join finds or creates the room and registers the client. A room that closes
// between lookup and registration is simply recreated.
func (h *Hub) join(ctx context.Context, boardID string, c *Client) (*Room, error) {
	for attempt := 0; attempt < 3; attempt++ {
		room, err := h.room(ctx, boardID)
		if err != nil {
			return nil, err
		}
		err = room.register(ctx, c)
		if errors.Is(err, ErrRoomClosed) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return room, nil
	}
	return nil, ErrRoomClosed
}

// room returns the ready room for a board, creating and loading it on first
// use. Concurrent callers for the same board wait for the same load.
func (h *Hub) room(ctx context.Context, boardID string) (*Room, error) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return nil, ErrShuttingDown
	}
	r, ok := h.rooms[boardID]
	if !ok {
		r = newRoom(h, boardID)
		h.rooms[boardID] = r
		h.roomsWG.Add(1)
		go func() {
			// The load uses its own context: it must finish even if the first
			// visitor disconnects, because other visitors may be waiting.
			loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.start(loadCtx)
		}()
	}
	h.mu.Unlock()

	select {
	case <-r.ready:
		if r.initErr != nil {
			return nil, r.initErr
		}
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// removeRoom forgets a room that has shut down.
func (h *Hub) removeRoom(r *Room) {
	h.mu.Lock()
	if h.rooms[r.id] == r {
		delete(h.rooms, r.id)
	}
	h.mu.Unlock()
}

// Stats reports live counters for health endpoints.
type Stats struct {
	Rooms   int `json:"rooms"`
	Clients int `json:"clients"`
}

// Stats returns a point-in-time view.
func (h *Hub) Stats() Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s := Stats{Rooms: len(h.rooms)}
	for _, r := range h.rooms {
		s.Clients += int(r.clientCount.Load())
	}
	return s
}

// Shutdown stops accepting joins, tells every client to reconnect elsewhere,
// flushes a final snapshot for each room and waits until all rooms are done or
// ctx expires.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	for _, r := range rooms {
		r.stop()
	}

	done := make(chan struct{})
	go func() { h.roomsWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
