package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

type testRec struct {
	model.Meta
	V int `json:"v"`
}

func rec(ts time.Time, v int) testRec {
	return testRec{Meta: model.NewMeta(model.KindHostMinute, ts), V: v}
}

func TestAppendFlushReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		if err := s.Append(model.KindHostMinute, rec(base.Add(time.Duration(i)*time.Minute), i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Rotate(); err != nil {
		t.Fatal(err)
	}
	segs, err := s.ListClosed()
	if err != nil || len(segs) != 1 {
		t.Fatalf("want 1 closed segment, got %d err=%v", len(segs), err)
	}
	if segs[0].Records != 100 || segs[0].Kind != model.KindHostMinute {
		t.Fatalf("bad meta: %+v", segs[0])
	}
	recs, truncated, err := ReadSegmentFile(s.ClosedPath(segs[0].Name))
	if err != nil || truncated || len(recs) != 100 {
		t.Fatalf("read: n=%d truncated=%v err=%v", len(recs), truncated, err)
	}
	var first testRec
	json.Unmarshal(recs[0], &first)
	if first.V != 0 || first.Schema != model.SchemaVersion {
		t.Fatalf("bad first record: %+v", first)
	}
}

func TestDateRotation(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	d1 := time.Date(2026, 7, 1, 23, 59, 0, 0, time.UTC)
	d2 := time.Date(2026, 7, 2, 0, 1, 0, 0, time.UTC)
	s.Append(model.KindHostMinute, rec(d1, 1))
	s.Append(model.KindHostMinute, rec(d2, 2)) // triggers rotation of day 1
	s.Rotate()
	segs, _ := s.ListClosed()
	if len(segs) != 2 {
		t.Fatalf("want 2 daily segments, got %d", len(segs))
	}
}

func TestCrashRecoveryTornTail(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		s.Append(model.KindSpike, rec(base.Add(time.Duration(i)*time.Minute), i))
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// Simulate a torn append: garbage bytes at the end of the active file
	// (a partially written gzip member from a forced kill).
	active := filepath.Join(dir, "spool", "active", model.KindSpike+".ndjson.gz")
	f, _ := os.OpenFile(active, os.O_WRONLY|os.O_APPEND, 0o640)
	f.Write([]byte{0x1f, 0x8b, 0x08, 0x00, 0xde, 0xad}) // truncated gzip header
	f.Close()

	// New store instance = agent restart. Recovery must preserve the 10
	// complete records and close the segment.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	segs, _ := s2.ListClosed()
	if len(segs) != 1 || !segs[0].Recovered {
		t.Fatalf("want 1 recovered segment, got %+v", segs)
	}
	if segs[0].Records != 10 {
		t.Fatalf("want all 10 pre-crash records recovered, got %d", segs[0].Records)
	}
	recs, _, _ := ReadSegmentFile(s2.ClosedPath(segs[0].Name))
	if len(recs) != 10 {
		t.Fatalf("re-read after recovery: %d", len(recs))
	}
}

func TestRetentionByAgeAndSize(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	old := time.Now().UTC().AddDate(0, 0, -200)
	s.Append(model.KindHostMinute, rec(old, 1))
	s.Rotate()
	s.Append(model.KindHostMinute, rec(time.Now().UTC(), 2))
	s.Rotate()
	res, err := s.EnforceRetention(0, 100)
	if err != nil || res.DeletedSegments != 1 {
		t.Fatalf("age retention: %+v err=%v", res, err)
	}
	segs, _ := s.ListClosed()
	if len(segs) != 1 {
		t.Fatalf("want only recent segment kept, got %d", len(segs))
	}
	// Size cap: force everything out.
	res, _ = s.EnforceRetention(1, 0)
	segs, _ = s.ListClosed()
	if res.DeletedSegments != 1 || len(segs) != 0 {
		t.Fatalf("size retention failed: %+v remaining=%d", res, len(segs))
	}
}

func TestSegmentSizeRotation(t *testing.T) {
	old := MaxSegmentBytes
	MaxSegmentBytes = 1 << 20
	defer func() { MaxSegmentBytes = old }()
	dir := t.TempDir()
	s, _ := Open(dir)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	big := make([]byte, 64*1024)
	for i := range big {
		big[i] = byte('a' + i%23)
	}
	type bigRec struct {
		model.Meta
		Blob string `json:"blob"`
	}
	for i := 0; i < 300; i++ {
		s.Append(model.KindProcTop, bigRec{model.NewMeta(model.KindProcTop, base.Add(time.Duration(i)*time.Second)), string(big)})
	}
	s.Rotate()
	segs, _ := s.ListClosed()
	if len(segs) < 2 {
		t.Fatalf("expected size-based rotation to produce multiple segments, got %d", len(segs))
	}
}
