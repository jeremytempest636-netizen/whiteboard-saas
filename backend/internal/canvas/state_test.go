package canvas

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func el(id string, ts int64, extra string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"ts":%d,"type":"rect"%s}`, id, ts, extra))
}

func TestApplyLastWriterWins(t *testing.T) {
	s := NewState()
	if got, err := s.Apply([]json.RawMessage{el("a", 100, `,"x":1`)}); err != nil || len(got) != 1 {
		t.Fatalf("first apply: %v %v", got, err)
	}
	if got, _ := s.Apply([]json.RawMessage{el("a", 90, `,"x":9`)}); len(got) != 0 {
		t.Fatalf("older write must be ignored, got %d", len(got))
	}
	if got, _ := s.Apply([]json.RawMessage{el("a", 110, `,"x":2`)}); len(got) != 1 {
		t.Fatalf("newer write must win")
	}
	if n := s.Len(); n != 1 {
		t.Fatalf("len = %d", n)
	}
}

func TestOrderIndependentConvergence(t *testing.T) {
	ops := []json.RawMessage{
		el("a", 100, `,"x":1`),
		el("a", 100, `,"x":2`), // same ts, different payload
		el("b", 50, ""),
		{},
	}
	ops[3] = json.RawMessage(`{"id":"b","ts":60,"deleted":true}`)

	forward, reverse := NewState(), NewState()
	forward.Apply(ops)
	rev := make([]json.RawMessage, len(ops))
	for i := range ops {
		rev[len(ops)-1-i] = ops[i]
	}
	reverse.Apply(rev)

	a, _, _, _ := forward.Snapshot(time.Now())
	b, _, _, _ := reverse.Snapshot(time.Now())
	var da, db Document
	_ = json.Unmarshal(a, &da)
	_ = json.Unmarshal(b, &db)
	if len(da.Elements) != len(db.Elements) {
		t.Fatalf("diverged: %d vs %d", len(da.Elements), len(db.Elements))
	}
	live := func(s *State) map[string]string {
		m := map[string]string{}
		for _, e := range s.Live() {
			id, _, _, _ := ParseHeader(e)
			m[id] = string(e)
		}
		return m
	}
	fa, fb := live(forward), live(reverse)
	if len(fa) != 1 || fa["a"] != fb["a"] {
		t.Fatalf("live sets differ: %v vs %v", fa, fb)
	}
}

func TestTombstoneBlocksResurrection(t *testing.T) {
	s := NewState()
	s.Apply([]json.RawMessage{json.RawMessage(`{"id":"a","ts":200,"deleted":true}`)})
	if got, _ := s.Apply([]json.RawMessage{el("a", 150, "")}); len(got) != 0 {
		t.Fatalf("stale update resurrected a deleted element")
	}
	if s.Len() != 0 {
		t.Fatalf("len = %d", s.Len())
	}
}

func TestClearWatermark(t *testing.T) {
	s := NewState()
	s.Apply([]json.RawMessage{el("a", 100, ""), el("b", 300, "")})
	if !s.Clear(200) {
		t.Fatal("clear should move the watermark")
	}
	if s.Len() != 1 {
		t.Fatalf("element newer than the clear must survive, len=%d", s.Len())
	}
	if got, _ := s.Apply([]json.RawMessage{el("late", 150, "")}); len(got) != 0 {
		t.Fatal("op older than the clear must be rejected")
	}
	if s.Clear(150) {
		t.Fatal("stale clear must be a no-op")
	}
}

func TestValidationAndLimits(t *testing.T) {
	s := NewState()
	bad := []json.RawMessage{
		json.RawMessage(`not json`),
		json.RawMessage(`{"ts":1}`),
		json.RawMessage(`{"id":"x"}`),
		el("ok", 10, ""),
	}
	applied, err := s.Apply(bad)
	if len(applied) != 1 || err == nil {
		t.Fatalf("applied=%d err=%v", len(applied), err)
	}

	full := NewState()
	batch := make([]json.RawMessage, 0, MaxElements+1)
	for i := 0; i < MaxElements+1; i++ {
		batch = append(batch, el(fmt.Sprintf("e%d", i), int64(i+1), ""))
	}
	_, err = full.Apply(batch)
	if err == nil || full.Len() != MaxElements {
		t.Fatalf("cap not enforced: len=%d err=%v", full.Len(), err)
	}
}

func TestSnapshotRoundTripAndPrune(t *testing.T) {
	s := NewState()
	now := time.Now()
	s.Apply([]json.RawMessage{
		el("keep", now.UnixMilli(), ""),
		json.RawMessage(fmt.Sprintf(`{"id":"old","ts":%d,"deleted":true}`, now.Add(-time.Hour).UnixMilli())),
		json.RawMessage(fmt.Sprintf(`{"id":"fresh","ts":%d,"deleted":true}`, now.UnixMilli())),
	})
	data, count, _, err := s.Snapshot(now)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var doc Document
	_ = json.Unmarshal(data, &doc)
	if len(doc.Elements) != 2 {
		t.Fatalf("old tombstone should be pruned, have %d elements", len(doc.Elements))
	}
	r := NewState()
	if err := r.Load(data); err != nil || r.Len() != 1 {
		t.Fatalf("load: len=%d err=%v", r.Len(), err)
	}
}

func TestRejectsFarFutureTimestamps(t *testing.T) {
	s := NewState()
	future := time.Now().Add(time.Hour).UnixMilli()
	applied, err := s.Apply([]json.RawMessage{el("x", future, "")})
	if len(applied) != 0 || err == nil {
		t.Fatalf("applied=%d err=%v", len(applied), err)
	}
	if s.Clear(future) {
		t.Fatal("clear far in the future must be ignored")
	}
}
