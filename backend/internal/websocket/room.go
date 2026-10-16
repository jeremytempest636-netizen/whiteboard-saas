package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"whiteboard/internal/canvas"
)

// Marker colours handed out to participants. They double as the pen colours of
// a physical whiteboard so each person's cursor reads like "their" marker.
var palette = []string{
	"#1f4fd8", // blue
	"#d93b30", // red
	"#1e8a5a", // green
	"#e08a00", // orange
	"#7a3fb5", // purple
	"#0f8fa3", // teal
	"#c23a82", // magenta
	"#5b6470", // graphite
}

const (
	presenceRefresh = 20 * time.Second
	maintenanceTick = 5 * time.Second
	busBuffer       = 4096
)

// inbound is something a client goroutine asks the room goroutine to do:
// either apply a message or send a reply back to that same client.
type inbound struct {
	from  *Client
	msg   clientMessage
	reply []byte
}

type registration struct {
	c    *Client
	resp chan error
}

// busEvent is queued for the room's Redis writer.
type busEvent struct {
	publish  *busMessage
	save     []json.RawMessage
	clear    int64
	presAdd  *Peer
	presDrop *Peer
}

// Room is the live session of one board on this node.
//
// A single goroutine (run) owns the client set, peer list and timers, so none
// of them need locks. Other goroutines communicate with it only through
// channels. The canvas state has its own lock because snapshot jobs read it
// from pool workers.
type Room struct {
	id  string
	hub *Hub
	log *zap.Logger

	state *canvas.State

	reg      chan registration
	unreg    chan *Client
	in       chan inbound
	remote   chan []byte
	outbox   chan busEvent
	stopReq  chan struct{}
	stopOnce sync.Once
	ready    chan struct{}
	done     chan struct{}
	writerWG chan struct{}

	initErr error

	// Owned by run().
	clients     map[*Client]struct{}
	remotePeers map[string]Peer

	clientCount atomic.Int64
	localPeers  atomic.Pointer[[]Peer]

	// Snapshot bookkeeping.
	savedRev   atomic.Uint64
	saving     atomic.Bool
	dirtySince time.Time
	snapArmed  bool
	snapTimer  *time.Timer
}

type closeInfo struct {
	code   int
	reason string
}

func newRoom(h *Hub, id string) *Room {
	r := &Room{
		id:          id,
		hub:         h,
		log:         h.log.With(zap.String("board", id)),
		state:       canvas.NewState(),
		reg:         make(chan registration),
		unreg:       make(chan *Client, 16),
		in:          make(chan inbound, 1024),
		remote:      make(chan []byte, 1024),
		outbox:      make(chan busEvent, busBuffer),
		stopReq:     make(chan struct{}),
		ready:       make(chan struct{}),
		done:        make(chan struct{}),
		writerWG:    make(chan struct{}),
		clients:     make(map[*Client]struct{}),
		remotePeers: make(map[string]Peer),
	}
	empty := []Peer{}
	r.localPeers.Store(&empty)
	return r
}

// start loads the board and launches the room goroutines. It runs once, in its
// own goroutine, and always closes r.ready.
func (r *Room) start(ctx context.Context) {
	defer close(r.ready)

	if err := r.hub.bus.Subscribe(ctx, r.id); err != nil {
		r.fail(err)
		return
	}
	if err := r.load(ctx); err != nil {
		r.hub.bus.Unsubscribe(ctx, r.id)
		r.fail(err)
		return
	}
	go r.writer()
	go r.run()
}

func (r *Room) fail(err error) {
	r.log.Error("room failed to start", zap.Error(err))
	r.initErr = err
	r.hub.removeRoom(r)
	close(r.done)
	r.hub.roomsWG.Done()
}

// load fills the canvas state: from the shared Redis copy when another node
// already hosts the board, otherwise from the latest Postgres snapshot.
func (r *Room) load(ctx context.Context) error {
	bus := r.hub.bus

	clearedAt, els, found, err := bus.LoadState(ctx, r.id)
	if err != nil {
		return err
	}
	if found {
		r.state.LoadElements(clearedAt, els)
	} else {
		data, err := r.hub.store.Load(ctx, r.id)
		if err != nil {
			return err
		}
		if len(data) > 0 {
			if err := r.state.Load(data); err != nil {
				return err
			}
			if err := bus.SeedState(ctx, r.id, r.state.ClearedAt(), r.state.Live()); err != nil {
				r.log.Warn("seed redis state failed", zap.Error(err))
			}
		}
	}
	r.savedRev.Store(r.state.Rev())

	peers, err := bus.PresenceList(ctx, r.id)
	if err != nil {
		r.log.Warn("presence load failed", zap.Error(err))
	}
	for _, p := range peers {
		r.remotePeers[p.ID] = p
	}
	return nil
}

// ---------------------------------------------------------------------------
// Entry points used by other goroutines.
// ---------------------------------------------------------------------------

// register adds a client and delivers the init frame.
func (r *Room) register(ctx context.Context, c *Client) error {
	req := registration{c: c, resp: make(chan error, 1)}
	select {
	case r.reg <- req:
	case <-r.done:
		return ErrRoomClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.resp:
		return err
	case <-r.done:
		return ErrRoomClosed
	}
}

// unregister tells the room a client's socket is gone.
func (r *Room) unregister(c *Client) {
	select {
	case r.unreg <- c:
	case <-r.done:
	}
}

// dispatch hands a client message to the room. It returns false when the room
// has shut down and the caller should stop reading.
func (r *Room) dispatch(in inbound) bool {
	select {
	case r.in <- in:
		return true
	case <-r.done:
		return false
	}
}

// deliverRemote queues a Redis message. It blocks briefly rather than dropping
// edits, but never indefinitely, so one stuck room cannot freeze the shared
// Pub/Sub reader.
func (r *Room) deliverRemote(payload []byte) {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case r.remote <- payload:
	case <-r.done:
	case <-t.C:
		r.log.Error("dropping remote message: room is overloaded")
	}
}

// stop asks the room to shut down (used on server shutdown).
func (r *Room) stop() {
	r.stopOnce.Do(func() { close(r.stopReq) })
}

// closeReason returns the WebSocket close code and text to send a client whose
// channel was closed. The room records the reason before closing the channel,
// so reading it afterwards is race-free.
func (r *Room) closeReason(c *Client) (int, string) {
	if ci, ok := c.closeInfo.Load().(closeInfo); ok {
		return ci.code, ci.reason
	}
	return websocket.CloseNormalClosure, "bye"
}

// ---------------------------------------------------------------------------
// The room goroutine.
// ---------------------------------------------------------------------------

func (r *Room) run() {
	idle := time.NewTimer(r.hub.opts.IdleTimeout)
	maint := time.NewTicker(maintenanceTick)
	r.snapTimer = time.NewTimer(time.Hour)
	r.snapTimer.Stop()
	defer maint.Stop()

	for {
		select {
		case req := <-r.reg:
			req.resp <- r.addClient(req.c)
			idle.Stop()

		case c := <-r.unreg:
			r.removeClient(c, 0, "")
			if len(r.clients) == 0 {
				idle.Reset(r.hub.opts.IdleTimeout)
			}

		case in := <-r.in:
			r.handleClient(in)

		case raw := <-r.remote:
			r.handleRemote(raw)

		case <-r.snapTimer.C:
			r.snapArmed = false
			r.persist()

		case <-maint.C:
			if r.state.Rev() != r.savedRev.Load() && !r.snapArmed {
				r.armSnapshot()
			}

		case <-idle.C:
			if len(r.clients) == 0 {
				r.shutdown(websocket.CloseGoingAway, "board idle")
				return
			}

		case <-r.stopReq:
			r.shutdown(websocket.CloseServiceRestart, "server restarting")
			return
		}
	}
}

func (r *Room) addClient(c *Client) error {
	if len(r.clients) >= r.hub.opts.MaxRoomClients {
		return ErrRoomFull
	}
	c.color = r.pickColor()
	r.clients[c] = struct{}{}
	r.clientCount.Store(int64(len(r.clients)))
	r.publishLocalPeers()

	init := frame(initMessage{
		Type:      TypeInit,
		ClientID:  c.id,
		You:       c.peer(),
		Els:       r.state.Live(),
		Peers:     r.allPeers(c),
		ClearedAt: r.state.ClearedAt(),
		Now:       time.Now().UnixMilli(),
	})
	if !c.trySend(init, false) {
		delete(r.clients, c)
		r.clientCount.Store(int64(len(r.clients)))
		return ErrRoomClosed
	}

	p := c.peer()
	r.broadcast(frame(joinMessage{Type: TypeJoin, Peer: p}), c, false)
	r.enqueue(busEvent{
		publish: &busMessage{Type: TypeJoin, From: c.id, Data: mustRaw(p)},
		presAdd: &p,
	})
	r.log.Debug("client joined", zap.String("client", c.id), zap.String("user", c.who.UserID), zap.Int("clients", len(r.clients)))
	return nil
}

// removeClient detaches a client (idempotent) and closes its send channel,
// which makes writePump send a close frame and stop.
func (r *Room) removeClient(c *Client, code int, reason string) {
	if _, ok := r.clients[c]; !ok {
		return
	}
	delete(r.clients, c)
	if code != 0 {
		c.closeInfo.Store(closeInfo{code: code, reason: reason})
	}
	close(c.send)
	r.clientCount.Store(int64(len(r.clients)))
	r.publishLocalPeers()

	p := c.peer()
	r.broadcast(frame(leaveMessage{Type: TypeLeave, ID: c.id}), nil, false)
	r.enqueue(busEvent{
		publish:  &busMessage{Type: TypeLeave, From: c.id, Data: mustRaw(p)},
		presDrop: &p,
	})
}

func (r *Room) handleClient(in inbound) {
	c := in.from
	if _, ok := r.clients[c]; !ok {
		return // dropped while the message was in flight
	}
	if in.reply != nil {
		if !c.trySend(in.reply, true) {
			r.removeClient(c, websocket.ClosePolicyViolation, "client too slow")
		}
		return
	}

	switch in.msg.Type {
	case TypeOp:
		if len(in.msg.Els) == 0 || len(in.msg.Els) > maxElementsPerOp {
			r.replyError(c, "bad_op", "op must contain between 1 and 500 elements")
			return
		}
		applied, err := r.state.Apply(in.msg.Els)
		if err != nil {
			r.replyError(c, "invalid_element", err.Error())
		}
		if len(applied) == 0 {
			return
		}
		r.broadcast(frame(opMessage{Type: TypeOp, From: c.id, Els: applied}), c, false)
		r.enqueue(busEvent{
			publish: &busMessage{Type: TypeOp, From: c.id, Data: mustRaw(applied)},
			save:    applied,
		})
		r.markDirty()

	case TypeClear:
		if !r.state.Clear(in.msg.TS) {
			return
		}
		r.broadcast(frame(clearMessage{Type: TypeClear, From: c.id, TS: in.msg.TS}), c, false)
		r.enqueue(busEvent{
			publish: &busMessage{Type: TypeClear, From: c.id, Data: mustRaw(clearData{TS: in.msg.TS})},
			clear:   in.msg.TS,
		})
		r.markDirty()

	case TypeCursor:
		r.broadcast(frame(cursorMessage{Type: TypeCursor, From: c.id, X: in.msg.X, Y: in.msg.Y}), c, true)
		r.enqueue(busEvent{publish: &busMessage{Type: TypeCursor, From: c.id, Data: mustRaw(cursorData{X: in.msg.X, Y: in.msg.Y})}})
	}
}

func (r *Room) replyError(c *Client, code, msg string) {
	if !c.trySend(frame(errorMessage{Type: TypeError, Code: code, Message: msg}), true) {
		r.removeClient(c, websocket.ClosePolicyViolation, "client too slow")
	}
}

// handleRemote applies a message that another node published.
func (r *Room) handleRemote(raw []byte) {
	var m busMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		r.log.Warn("bad bus message", zap.Error(err))
		return
	}
	if m.Node == r.hub.opts.NodeID {
		return // our own publish echoing back
	}

	switch m.Type {
	case TypeOp:
		var els []json.RawMessage
		if json.Unmarshal(m.Data, &els) != nil {
			return
		}
		applied, _ := r.state.Apply(els)
		if len(applied) > 0 {
			r.broadcast(frame(opMessage{Type: TypeOp, From: m.From, Els: applied}), nil, false)
			r.markDirty()
		}

	case TypeClear:
		var d clearData
		if json.Unmarshal(m.Data, &d) != nil {
			return
		}
		if r.state.Clear(d.TS) {
			r.broadcast(frame(clearMessage{Type: TypeClear, From: m.From, TS: d.TS}), nil, false)
			r.markDirty()
		}

	case TypeCursor:
		var d cursorData
		if json.Unmarshal(m.Data, &d) != nil {
			return
		}
		r.broadcast(frame(cursorMessage{Type: TypeCursor, From: m.From, X: d.X, Y: d.Y}), nil, true)

	case TypeJoin:
		var p Peer
		if json.Unmarshal(m.Data, &p) != nil || p.ID == "" {
			return
		}
		r.remotePeers[p.ID] = p
		r.broadcast(frame(joinMessage{Type: TypeJoin, Peer: p}), nil, false)

	case TypeLeave:
		var p Peer
		if json.Unmarshal(m.Data, &p) != nil || p.ID == "" {
			return
		}
		if _, ok := r.remotePeers[p.ID]; ok {
			delete(r.remotePeers, p.ID)
			r.broadcast(frame(leaveMessage{Type: TypeLeave, ID: p.ID}), nil, false)
		}

	case typePresence:
		var fresh []Peer
		if json.Unmarshal(m.Data, &fresh) != nil {
			return
		}
		r.reconcilePeers(fresh)
	}
}

// reconcilePeers replaces the remote peer set with the authoritative list from
// Redis and tells local clients about the difference. This is what removes the
// participants of a node that crashed without saying goodbye.
func (r *Room) reconcilePeers(fresh []Peer) {
	local := make(map[string]struct{}, len(r.clients))
	for c := range r.clients {
		local[c.id] = struct{}{}
	}
	next := make(map[string]Peer, len(fresh))
	for _, p := range fresh {
		if _, mine := local[p.ID]; !mine {
			next[p.ID] = p
		}
	}
	for id := range r.remotePeers {
		if _, still := next[id]; !still {
			r.broadcast(frame(leaveMessage{Type: TypeLeave, ID: id}), nil, false)
		}
	}
	for id, p := range next {
		if _, known := r.remotePeers[id]; !known {
			r.broadcast(frame(joinMessage{Type: TypeJoin, Peer: p}), nil, false)
		}
	}
	r.remotePeers = next
}

// broadcast queues a frame for every local client except skip. A client whose
// buffer is full is dropped so it cannot stall the room.
func (r *Room) broadcast(msg []byte, skip *Client, droppable bool) {
	var slow []*Client
	for c := range r.clients {
		if c == skip {
			continue
		}
		if !c.trySend(msg, droppable) {
			slow = append(slow, c)
		}
	}
	for _, c := range slow {
		r.log.Warn("dropping slow client", zap.String("client", c.id))
		r.removeClient(c, websocket.ClosePolicyViolation, "client too slow")
	}
}

func (r *Room) allPeers(except *Client) []Peer {
	out := make([]Peer, 0, len(r.clients)+len(r.remotePeers))
	for c := range r.clients {
		if c != except {
			out = append(out, c.peer())
		}
	}
	for _, p := range r.remotePeers {
		out = append(out, p)
	}
	return out
}

// pickColor returns the palette colour used by the fewest participants.
func (r *Room) pickColor() string {
	use := make(map[string]int, len(palette))
	for c := range r.clients {
		use[c.color]++
	}
	for _, p := range r.remotePeers {
		use[p.Color]++
	}
	best, bestN := palette[0], int(^uint(0)>>1)
	for _, col := range palette {
		if n := use[col]; n < bestN {
			best, bestN = col, n
		}
	}
	return best
}

func (r *Room) publishLocalPeers() {
	peers := make([]Peer, 0, len(r.clients))
	for c := range r.clients {
		peers = append(peers, c.peer())
	}
	r.localPeers.Store(&peers)
}

// ---------------------------------------------------------------------------
// Redis writer: one goroutine per room preserves the order of events.
// ---------------------------------------------------------------------------

func (r *Room) enqueue(ev busEvent) {
	select {
	case r.outbox <- ev:
	default:
		r.log.Error("redis outbox full; event dropped", zap.Int("buffer", busBuffer))
	}
}

func (r *Room) writer() {
	defer close(r.writerWG)
	bus := r.hub.bus
	tick := time.NewTicker(presenceRefresh)
	defer tick.Stop()

	call := func(fn func(ctx context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := fn(ctx); err != nil {
			r.log.Warn("redis call failed", zap.Error(err))
		}
	}

	for {
		select {
		case ev, ok := <-r.outbox:
			if !ok {
				return
			}
			if ev.publish != nil {
				call(func(ctx context.Context) error { return bus.Publish(ctx, r.id, *ev.publish) })
			}
			if len(ev.save) > 0 {
				call(func(ctx context.Context) error { return bus.SaveElements(ctx, r.id, ev.save) })
			}
			if ev.clear > 0 {
				call(func(ctx context.Context) error { return bus.ClearState(ctx, r.id, ev.clear) })
			}
			if ev.presAdd != nil {
				call(func(ctx context.Context) error { return bus.PresenceAdd(ctx, r.id, *ev.presAdd) })
			}
			if ev.presDrop != nil {
				call(func(ctx context.Context) error { return bus.PresenceRemove(ctx, r.id, *ev.presDrop) })
			}

		case <-tick.C:
			call(func(ctx context.Context) error {
				if local := *r.localPeers.Load(); len(local) > 0 {
					if err := bus.PresenceAdd(ctx, r.id, local...); err != nil {
						return err
					}
				}
				peers, err := bus.PresenceList(ctx, r.id)
				if err != nil {
					return err
				}
				// Feed the authoritative list back through the room goroutine.
				payload, _ := json.Marshal(busMessage{Node: "presence", Type: typePresence, Data: mustRaw(peers)})
				select {
				case r.remote <- payload:
				default:
				}
				return nil
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Snapshots: debounce + max-wait, executed on the shared worker pool.
// ---------------------------------------------------------------------------

func (r *Room) markDirty() {
	if r.dirtySince.IsZero() {
		r.dirtySince = time.Now()
	}
	r.armSnapshot()
}

// armSnapshot (re)starts the debounce timer. Each new edit pushes the save
// back by SnapshotDebounce, but never past SnapshotMaxWait since the first
// unsaved edit, so a constantly busy board is still persisted regularly.
func (r *Room) armSnapshot() {
	if r.dirtySince.IsZero() {
		r.dirtySince = time.Now()
	}
	wait := r.hub.opts.SnapshotDebounce
	if left := r.hub.opts.SnapshotMaxWait - time.Since(r.dirtySince); left < wait {
		wait = left
	}
	if wait < 0 {
		wait = 0
	}
	r.snapTimer.Reset(wait)
	r.snapArmed = true
}

// persist submits a snapshot job to the worker pool.
func (r *Room) persist() {
	rev := r.state.Rev()
	if rev == r.savedRev.Load() {
		r.dirtySince = time.Time{}
		return
	}
	if r.saving.Load() {
		r.armSnapshot() // a save is in flight; try again after the debounce
		return
	}
	r.saving.Store(true)
	r.dirtySince = time.Time{}

	ok := r.hub.pool.Submit(func() {
		defer r.saving.Store(false)
		if err := r.saveNow(); err != nil {
			r.log.Error("snapshot failed; will retry", zap.Error(err))
		}
	})
	if !ok {
		r.saving.Store(false)
		r.log.Warn("snapshot queue full; retrying")
		r.armSnapshot()
	}
}

// saveNow serialises the canvas and writes it to Postgres.
func (r *Room) saveNow() error {
	data, count, rev, err := r.state.Snapshot(time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.hub.store.Save(ctx, r.id, data, count); err != nil {
		return err
	}
	r.savedRev.Store(rev)
	return nil
}

// shutdown closes every client, flushes the final snapshot and releases
// resources. It runs on the room goroutine as its last act.
func (r *Room) shutdown(code int, reason string) {
	r.hub.removeRoom(r)
	for c := range r.clients {
		r.removeClient(c, code, reason)
	}
	close(r.outbox)
	<-r.writerWG // all queued Redis writes are flushed

	if r.state.Rev() != r.savedRev.Load() {
		if err := r.saveNow(); err != nil {
			r.log.Error("final snapshot failed", zap.Error(err))
		}
	}
	r.snapTimer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.hub.bus.Unsubscribe(ctx, r.id)

	close(r.done)
	r.hub.roomsWG.Done()
	r.log.Info("room closed", zap.Int("elements", r.state.Len()))
}
