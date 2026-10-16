// Package canvas holds the authoritative in-memory model of a whiteboard.
//
// It is deliberately free of third-party imports: the replication rules live
// here, so they can be unit-tested without a database or Redis.
//
// Conflict resolution is last-writer-wins per element:
//
//   - every element carries an id and a millisecond timestamp (ts) set by the
//     client that last touched it;
//   - an incoming element replaces the stored one only when its ts is greater
//     (ties are broken by comparing the raw bytes, so every node converges on
//     the same winner regardless of arrival order);
//   - deletions are tombstones (deleted:true) so a late, older update cannot
//     resurrect an erased shape;
//   - "clear board" raises a watermark (clearedAt); anything at or below it is
//     rejected, which makes the operation commutative with concurrent edits.
//
// Because the rules are order-independent, ops can travel through Redis
// Pub/Sub, worker pools and several server nodes without a global lock.
package canvas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// MaxElementBytes bounds one serialized element (a long freehand stroke).
	MaxElementBytes = 128 * 1024
	// MaxElements bounds live elements per board.
	MaxElements = 5000
	// MaxIDLength bounds element ids.
	MaxIDLength = 64
	// TombstoneTTL is how long deletion markers are kept for conflict checks.
	TombstoneTTL = 10 * time.Minute
	// MaxFutureSkew rejects timestamps from clients with badly wrong clocks.
	// Without it, one element stamped far in the future could never be edited
	// again, because every later edit would lose the last-writer-wins race.
	MaxFutureSkew = 5 * time.Minute
)

// Errors returned by Apply for a single malformed element.
var (
	ErrInvalidElement = errors.New("canvas: invalid element")
	ErrBoardFull      = errors.New("canvas: board is full")
)

// header is the subset of an element the server needs to understand.
type header struct {
	ID      string `json:"id"`
	TS      int64  `json:"ts"`
	Deleted bool   `json:"deleted"`
}

type entry struct {
	TS      int64
	Deleted bool
	Raw     json.RawMessage
}

// State is a concurrency-safe LWW element map.
type State struct {
	mu        sync.RWMutex
	els       map[string]entry
	clearedAt int64
	rev       uint64
	live      int
}

// NewState returns an empty board.
func NewState() *State {
	return &State{els: make(map[string]entry)}
}

// ParseHeader validates an element and returns its identity.
func ParseHeader(raw json.RawMessage) (id string, ts int64, deleted bool, err error) {
	if len(raw) == 0 || len(raw) > MaxElementBytes {
		return "", 0, false, fmt.Errorf("%w: size %d", ErrInvalidElement, len(raw))
	}
	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", 0, false, fmt.Errorf("%w: %v", ErrInvalidElement, err)
	}
	if h.ID == "" || len(h.ID) > MaxIDLength {
		return "", 0, false, fmt.Errorf("%w: bad id", ErrInvalidElement)
	}
	if h.TS <= 0 {
		return "", 0, false, fmt.Errorf("%w: bad ts", ErrInvalidElement)
	}
	return h.ID, h.TS, h.Deleted, nil
}

func tombstone(id string, ts int64) json.RawMessage {
	b, _ := json.Marshal(header{ID: id, TS: ts, Deleted: true})
	return b
}

// Apply merges elements into the board and returns the ones that changed
// state, in the form they should be re-broadcast. Invalid elements are skipped
// and reported through the returned error (joined) while valid ones still
// apply, so one bad shape never blocks a batch.
func (s *State) Apply(elements []json.RawMessage) ([]json.RawMessage, error) {
	var (
		applied []json.RawMessage
		errs    []error
	)
	limit := time.Now().Add(MaxFutureSkew).UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, raw := range elements {
		id, ts, deleted, err := ParseHeader(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ts > limit {
			errs = append(errs, fmt.Errorf("%w: ts too far in the future", ErrInvalidElement))
			continue
		}
		if ts <= s.clearedAt {
			continue
		}
		cur, exists := s.els[id]
		if exists && !wins(ts, raw, cur) {
			continue
		}
		if !exists && !deleted && s.live >= MaxElements {
			errs = append(errs, ErrBoardFull)
			continue
		}

		next := entry{TS: ts, Deleted: deleted}
		if deleted {
			next.Raw = tombstone(id, ts)
		} else {
			next.Raw = append(json.RawMessage(nil), raw...)
		}

		switch {
		case !exists && !deleted:
			s.live++
		case exists && cur.Deleted && !deleted:
			s.live++
		case exists && !cur.Deleted && deleted:
			s.live--
		}
		s.els[id] = next
		s.rev++
		applied = append(applied, next.Raw)
	}
	return applied, errors.Join(errs...)
}

// wins reports whether (ts, raw) should replace cur.
func wins(ts int64, raw json.RawMessage, cur entry) bool {
	if ts != cur.TS {
		return ts > cur.TS
	}
	return bytes.Compare(raw, cur.Raw) > 0
}

// Clear wipes the board and raises the watermark. It reports whether the
// watermark moved (a stale clear, or one stamped too far ahead, is a no-op).
func (s *State) Clear(ts int64) bool {
	if ts <= 0 || ts > time.Now().Add(MaxFutureSkew).UnixMilli() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts <= s.clearedAt {
		return false
	}
	s.clearedAt = ts
	for id, e := range s.els {
		if e.TS <= ts {
			delete(s.els, id)
		}
	}
	s.live = 0
	for _, e := range s.els {
		if !e.Deleted {
			s.live++
		}
	}
	s.rev++
	return true
}

// Live returns the visible elements (tombstones excluded).
func (s *State) Live() []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]json.RawMessage, 0, s.live)
	for _, e := range s.els {
		if !e.Deleted {
			out = append(out, e.Raw)
		}
	}
	return out
}

// ClearedAt returns the current clear watermark.
func (s *State) ClearedAt() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clearedAt
}

// Rev increases on every change; used to skip redundant snapshots.
func (s *State) Rev() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rev
}

// Len returns the number of visible elements.
func (s *State) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live
}

// Document is the persisted JSON shape of a board snapshot.
type Document struct {
	Version   int               `json:"version"`
	ClearedAt int64             `json:"clearedAt"`
	Elements  []json.RawMessage `json:"elements"`
}

// Snapshot serializes the board for persistence. Old tombstones are pruned
// and the returned revision identifies the state that was captured.
func (s *State) Snapshot(now time.Time) (data []byte, count int, rev uint64, err error) {
	cutoff := now.Add(-TombstoneTTL).UnixMilli()

	s.mu.Lock()
	for id, e := range s.els {
		if e.Deleted && e.TS < cutoff {
			delete(s.els, id)
		}
	}
	doc := Document{Version: 1, ClearedAt: s.clearedAt, Elements: make([]json.RawMessage, 0, len(s.els))}
	for _, e := range s.els {
		doc.Elements = append(doc.Elements, e.Raw)
		if !e.Deleted {
			count++
		}
	}
	rev = s.rev
	s.mu.Unlock()

	data, err = json.Marshal(doc)
	return data, count, rev, err
}

// Load replaces the board with a persisted document. Invalid elements are
// dropped rather than failing the whole load.
func (s *State) Load(data []byte) error {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("canvas: decode snapshot: %w", err)
	}
	s.LoadElements(doc.ClearedAt, doc.Elements)
	return nil
}

// LoadElements replaces the board from already-decoded parts.
func (s *State) LoadElements(clearedAt int64, elements []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.els = make(map[string]entry, len(elements))
	s.clearedAt = clearedAt
	s.live = 0
	for _, raw := range elements {
		id, ts, deleted, err := ParseHeader(raw)
		if err != nil || ts <= clearedAt {
			continue
		}
		e := entry{TS: ts, Deleted: deleted, Raw: append(json.RawMessage(nil), raw...)}
		if deleted {
			e.Raw = tombstone(id, ts)
		} else {
			s.live++
		}
		s.els[id] = e
	}
	s.rev++
}
