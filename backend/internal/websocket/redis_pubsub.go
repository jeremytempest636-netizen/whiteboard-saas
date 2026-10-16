package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"whiteboard/internal/canvas"
)

// Redis layout, per board id. The id is wrapped in {braces} so that, on a
// Redis Cluster, every key of one board hashes to the same slot and
// multi-key pipelines stay legal.
//
//	wb:{id}:ch        Pub/Sub channel carrying busMessage JSON between nodes
//	wb:{id}:els       hash  elementID -> element JSON (+ "__cleared" watermark)
//	wb:{id}:peers     zset  peer JSON  -> last-seen unix seconds
const (
	clearedField = "__cleared"
	stateTTL     = 6 * time.Hour
	presenceTTL  = 60 * time.Second // a peer not refreshed for this long is gone
)

func channelFor(boardID string) string  { return "wb:{" + boardID + "}:ch" }
func stateKey(boardID string) string    { return "wb:{" + boardID + "}:els" }
func presenceKey(boardID string) string { return "wb:{" + boardID + "}:peers" }

// boardFromChannel extracts the board id from a channel name.
func boardFromChannel(ch string) (string, bool) {
	open := strings.IndexByte(ch, '{')
	end := strings.IndexByte(ch, '}')
	if open < 0 || end <= open+1 {
		return "", false
	}
	return ch[open+1 : end], true
}

// luaSetIfNewer stores an element only when it is newer than what the hash
// already holds, so out-of-order writes from different nodes cannot move the
// Redis copy backwards. KEYS[1]=hash, ARGV[1]=id, ARGV[2]=ts, ARGV[3]=json.
const luaSetIfNewer = `
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur then
  local ok, decoded = pcall(cjson.decode, cur)
  if ok and decoded.ts then
    local cts = tonumber(decoded.ts)
    local nts = tonumber(ARGV[2])
    if cts > nts or (cts == nts and cur >= ARGV[3]) then return 0 end
  end
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[3])
return 1`

// luaClear raises the clear watermark and drops elements that are not newer
// than it (matching canvas.State.Clear). KEYS[1]=hash, ARGV[1]=ts.
const luaClear = `
local cur = tonumber(redis.call('HGET', KEYS[1], '__cleared') or '0')
local nts = tonumber(ARGV[1])
if cur >= nts then return 0 end
local all = redis.call('HGETALL', KEYS[1])
for i = 1, #all, 2 do
  local field = all[i]
  if field ~= '__cleared' then
    local ok, decoded = pcall(cjson.decode, all[i + 1])
    if not ok or not decoded.ts or tonumber(decoded.ts) <= nts then
      redis.call('HDEL', KEYS[1], field)
    end
  end
end
redis.call('HSET', KEYS[1], '__cleared', ARGV[1])
return 1`

// Bus is the Redis adapter. It carries live room traffic between server nodes
// (Pub/Sub), keeps a short-lived copy of each active board so that a node that
// joins late starts from the current state rather than the last Postgres
// snapshot, and tracks who is present.
type Bus struct {
	rdb    redis.UniversalClient
	nodeID string
	log    *zap.Logger

	mu      sync.Mutex
	refs    map[string]int // channel -> number of local rooms using it
	ps      *redis.PubSub
	handler func(channel, payload string)

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewBus connects the Pub/Sub reader. handler is invoked, in order, for every
// message received; it must not block for long.
func NewBus(rdb redis.UniversalClient, nodeID string, log *zap.Logger, handler func(channel, payload string)) (*Bus, error) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bus{
		rdb:     rdb,
		nodeID:  nodeID,
		log:     log.Named("bus"),
		refs:    make(map[string]int),
		handler: handler,
		cancel:  cancel,
	}
	// Subscribing to a node-private channel guarantees the connection exists
	// before the first room subscribes.
	b.ps = rdb.Subscribe(ctx, "wb:node:"+nodeID)
	if _, err := b.ps.Receive(ctx); err != nil {
		cancel()
		_ = b.ps.Close()
		return nil, fmt.Errorf("redis pubsub: %w", err)
	}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for msg := range b.ps.Channel(redis.WithChannelSize(4096)) {
			b.handler(msg.Channel, msg.Payload)
		}
	}()
	return b, nil
}

// Subscribe starts delivering a board's channel. Calls are reference counted
// because a room can be torn down and re-created on the same node within a
// few milliseconds.
func (b *Bus) Subscribe(ctx context.Context, boardID string) error {
	ch := channelFor(boardID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refs[ch] == 0 {
		if err := b.ps.Subscribe(ctx, ch); err != nil {
			return err
		}
	}
	b.refs[ch]++
	return nil
}

// Unsubscribe releases one reference to a board's channel.
func (b *Bus) Unsubscribe(ctx context.Context, boardID string) {
	ch := channelFor(boardID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refs[ch] == 0 {
		return
	}
	b.refs[ch]--
	if b.refs[ch] == 0 {
		delete(b.refs, ch)
		if err := b.ps.Unsubscribe(ctx, ch); err != nil {
			b.log.Warn("unsubscribe failed", zap.String("board", boardID), zap.Error(err))
		}
	}
}

// Publish sends a message to every node (including this one, which ignores it
// by node id).
func (b *Bus) Publish(ctx context.Context, boardID string, m busMessage) error {
	m.Node = b.nodeID
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.rdb.Publish(ctx, channelFor(boardID), payload).Err()
}

// LoadState returns the live copy of a board, if one exists.
func (b *Bus) LoadState(ctx context.Context, boardID string) (clearedAt int64, els []json.RawMessage, found bool, err error) {
	all, err := b.rdb.HGetAll(ctx, stateKey(boardID)).Result()
	if err != nil {
		return 0, nil, false, err
	}
	if len(all) == 0 {
		return 0, nil, false, nil
	}
	els = make([]json.RawMessage, 0, len(all))
	for field, val := range all {
		if field == clearedField {
			clearedAt, _ = strconv.ParseInt(val, 10, 64)
			continue
		}
		els = append(els, json.RawMessage(val))
	}
	return clearedAt, els, true, nil
}

// SaveElements writes elements to the shared live copy (newest wins).
func (b *Bus) SaveElements(ctx context.Context, boardID string, els []json.RawMessage) error {
	if len(els) == 0 {
		return nil
	}
	key := stateKey(boardID)
	pipe := b.rdb.Pipeline()
	for _, raw := range els {
		id, ts, _, err := canvas.ParseHeader(raw)
		if err != nil {
			continue
		}
		pipe.Eval(ctx, luaSetIfNewer, []string{key}, id, ts, string(raw))
	}
	pipe.Expire(ctx, key, stateTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ClearState applies a clear to the shared live copy.
func (b *Bus) ClearState(ctx context.Context, boardID string, ts int64) error {
	key := stateKey(boardID)
	pipe := b.rdb.Pipeline()
	pipe.Eval(ctx, luaClear, []string{key}, ts)
	pipe.Expire(ctx, key, stateTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// SeedState copies a state loaded from Postgres into Redis so other nodes can
// start from it.
func (b *Bus) SeedState(ctx context.Context, boardID string, clearedAt int64, els []json.RawMessage) error {
	if clearedAt > 0 {
		if err := b.ClearState(ctx, boardID, clearedAt); err != nil {
			return err
		}
	}
	return b.SaveElements(ctx, boardID, els)
}

// PresenceAdd marks peers as present (and refreshes them).
func (b *Bus) PresenceAdd(ctx context.Context, boardID string, peers ...Peer) error {
	if len(peers) == 0 {
		return nil
	}
	key := presenceKey(boardID)
	now := float64(time.Now().Unix())
	zs := make([]redis.Z, 0, len(peers))
	for _, p := range peers {
		zs = append(zs, redis.Z{Score: now, Member: string(frame(p))})
	}
	pipe := b.rdb.Pipeline()
	pipe.ZAdd(ctx, key, zs...)
	pipe.Expire(ctx, key, 2*presenceTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// PresenceRemove removes a peer.
func (b *Bus) PresenceRemove(ctx context.Context, boardID string, p Peer) error {
	return b.rdb.ZRem(ctx, presenceKey(boardID), string(frame(p))).Err()
}

// PresenceList returns peers seen within presenceTTL and prunes the rest, so
// the entries of a node that crashed disappear on their own.
func (b *Bus) PresenceList(ctx context.Context, boardID string) ([]Peer, error) {
	key := presenceKey(boardID)
	cutoff := time.Now().Add(-presenceTTL).Unix()

	pipe := b.rdb.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", "("+strconv.FormatInt(cutoff, 10))
	members := pipe.ZRange(ctx, key, 0, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	raw, err := members.Result()
	if err != nil {
		return nil, err
	}
	peers := make([]Peer, 0, len(raw))
	for _, s := range raw {
		var p Peer
		if json.Unmarshal([]byte(s), &p) == nil && p.ID != "" {
			peers = append(peers, p)
		}
	}
	return peers, nil
}

// Close stops the reader goroutine and the Pub/Sub connection.
func (b *Bus) Close() {
	b.cancel()
	_ = b.ps.Close()
	b.wg.Wait()
}
