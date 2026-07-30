// Package store implements the agent's local telemetry spool.
//
// Design (see docs/ARCHITECTURE.md for the full rationale):
//
//   - Records are NDJSON, one kind per segment file, gzip-compressed.
//   - The active segment per kind is an append-only file of *concatenated gzip
//     members*: each flush writes one complete member with a single append
//     write. A crash can only tear the final member; readers use gzip
//     multistream decoding and recover every complete record before the tear.
//   - Segments rotate on UTC date change or size cap and are moved into
//     closed/ with an atomic rename, together with a sidecar .meta.json
//     (record count, time range) used to build the export manifest.
//   - Corruption is isolated per segment: one damaged day/kind never affects
//     the rest of the monitoring period.
//   - Retention enforces a total byte cap and maximum age by deleting the
//     oldest closed segments first, and reports what was dropped.
package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/model"
)

const (
	activeDir = "active"
	closedDir = "closed"

	// autoFlushRecords bounds the in-memory buffer per kind (no unbounded queues).
	autoFlushRecords = 512
)

// MaxSegmentBytes caps a single segment (compressed bytes written plus raw
// bytes buffered, a conservative estimate) so one file never grows unbounded.
// Variable so tests can lower it.
var MaxSegmentBytes int64 = 32 << 20

// SegmentInfo describes a closed segment (sidecar metadata).
type SegmentInfo struct {
	Name      string    `json:"name"` // file name within closed/
	Kind      string    `json:"kind"`
	Records   int       `json:"records"`
	MinTS     time.Time `json:"min_ts"`
	MaxTS     time.Time `json:"max_ts"`
	SizeBytes int64     `json:"size_bytes"`
	Recovered bool      `json:"recovered,omitempty"`
}

type activeSeg struct {
	date        string // UTC YYYYMMDD the segment was opened for
	seq         int
	buf         []json.RawMessage
	records     int
	minTS       time.Time
	maxTS       time.Time
	size        int64 // compressed bytes written to disk
	rawBuffered int64 // raw bytes currently buffered (conservative size bound)
}

// Store is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	root    string // <dataDir>/spool
	active  map[string]*activeSeg
	dropped uint64
}

// Open initializes the spool and recovers any segments left active by a
// previous run (forced termination, reboot).
func Open(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "spool")
	for _, d := range []string{filepath.Join(root, activeDir), filepath.Join(root, closedDir)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
	}
	s := &Store{root: root, active: map[string]*activeSeg{}}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// recover moves any leftover active files into closed/, rebuilding sidecar
// metadata by re-reading every complete record (torn tails are dropped).
// Crash-orphaned temporary files (.tmp from interrupted sidecar/rename
// operations) are removed first so they can never shadow real segments.
func (s *Store) recover() error {
	for _, dir := range []string{filepath.Join(s.root, activeDir), filepath.Join(s.root, closedDir)} {
		if tmps, err := filepath.Glob(filepath.Join(dir, "*.tmp")); err == nil {
			for _, t := range tmps {
				os.Remove(t)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.root, activeDir))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		src := filepath.Join(s.root, activeDir, e.Name())
		kind := strings.TrimSuffix(e.Name(), ".ndjson.gz")
		recs, _, _ := ReadSegmentFile(src) // tolerate torn tail
		info := SegmentInfo{Kind: kind, Records: len(recs), Recovered: true}
		for _, r := range recs {
			var m model.Meta
			if json.Unmarshal(r, &m) == nil {
				if info.MinTS.IsZero() || m.TS.Before(info.MinTS) {
					info.MinTS = m.TS
				}
				if m.TS.After(info.MaxTS) {
					info.MaxTS = m.TS
				}
			}
		}
		if err := s.closeFile(src, kind, info); err != nil {
			return fmt.Errorf("recover %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Append buffers a record for the given kind. The record must marshal to JSON
// and embed model.Meta. Buffers are bounded: when full they are flushed; if a
// flush fails (e.g. disk full) the oldest buffered records are dropped and
// counted rather than growing without bound.
func (s *Store) Append(kind string, rec any) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var m model.Meta
	if err := json.Unmarshal(b, &m); err != nil || m.TS.IsZero() {
		return fmt.Errorf("record for kind %s lacks meta timestamp", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.active[kind]
	date := m.TS.UTC().Format("20060102")
	if a != nil && (a.date != date || a.size+a.rawBuffered > MaxSegmentBytes) {
		if err := s.rotateLocked(kind); err != nil {
			return err
		}
		a = nil
	}
	if a == nil {
		a = &activeSeg{date: date}
		s.active[kind] = a
	}
	a.buf = append(a.buf, b)
	a.rawBuffered += int64(len(b))
	a.records++
	if a.minTS.IsZero() || m.TS.Before(a.minTS) {
		a.minTS = m.TS
	}
	if m.TS.After(a.maxTS) {
		a.maxTS = m.TS
	}
	if len(a.buf) >= autoFlushRecords {
		if err := s.flushLocked(kind); err != nil {
			// Disk trouble: drop this batch, keep counting. Collection must
			// not consume unbounded memory when the disk is unavailable.
			s.dropped += uint64(len(a.buf))
			a.records -= len(a.buf)
			a.buf = a.buf[:0]
			return err
		}
	}
	return nil
}

// Flush writes all buffered records as one gzip member per kind.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for kind := range s.active {
		if err := s.flushLocked(kind); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) flushLocked(kind string) error {
	a := s.active[kind]
	if a == nil || len(a.buf) == 0 {
		return nil
	}
	var member bytes.Buffer
	zw := gzip.NewWriter(&member)
	for _, rec := range a.buf {
		zw.Write(rec)
		zw.Write([]byte("\n"))
	}
	if err := zw.Close(); err != nil {
		return err
	}
	p := s.activePath(kind)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(member.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	a.size += int64(member.Len())
	a.buf = a.buf[:0]
	a.rawBuffered = 0
	return nil
}

// Rotate closes the active segment of every kind (used before export and on
// clean shutdown) and returns nothing; closed segments become visible to
// ListClosed.
func (s *Store) Rotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for kind := range s.active {
		if err := s.rotateLocked(kind); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Store) rotateLocked(kind string) error {
	a := s.active[kind]
	if a == nil {
		return nil
	}
	if err := s.flushLocked(kind); err != nil {
		return err
	}
	delete(s.active, kind)
	if a.records == 0 {
		os.Remove(s.activePath(kind)) // nothing written
		return nil
	}
	info := SegmentInfo{Kind: kind, Records: a.records, MinTS: a.minTS, MaxTS: a.maxTS}
	return s.closeFile(s.activePath(kind), kind, info)
}

// closeFile moves an active file into closed/ with a unique sequenced name and
// writes its sidecar metadata atomically.
func (s *Store) closeFile(src, kind string, info SegmentInfo) error {
	date := info.MinTS.UTC().Format("20060102")
	if info.MinTS.IsZero() {
		date = time.Now().UTC().Format("20060102")
	}
	var dst string
	for seq := 0; ; seq++ {
		name := fmt.Sprintf("seg-%s-%s-%03d.ndjson.gz", kind, date, seq)
		dst = filepath.Join(s.root, closedDir, name)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			info.Name = name
			break
		}
	}
	if st, err := os.Stat(src); err == nil {
		info.SizeBytes = st.Size()
	} else {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	mb, _ := json.Marshal(info)
	tmp := dst + ".meta.json.tmp"
	if err := os.WriteFile(tmp, mb, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, dst+".meta.json")
}

func (s *Store) activePath(kind string) string {
	return filepath.Join(s.root, activeDir, kind+".ndjson.gz")
}

// ListClosed returns closed segments sorted oldest-first.
func (s *Store) ListClosed() ([]SegmentInfo, error) {
	dir := filepath.Join(s.root, closedDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []SegmentInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info SegmentInfo
		if json.Unmarshal(b, &info) == nil && info.Name != "" {
			if _, err := os.Stat(filepath.Join(dir, info.Name)); err == nil {
				out = append(out, info)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ClosedPath returns the absolute path of a closed segment.
func (s *Store) ClosedPath(name string) string {
	return filepath.Join(s.root, closedDir, name)
}

// Remove deletes a closed segment and its sidecar (post-export cleanup).
func (s *Store) Remove(name string) error {
	p := s.ClosedPath(name)
	err := os.Remove(p)
	os.Remove(p + ".meta.json")
	return err
}

// Dropped returns the count of records dropped due to disk pressure.
func (s *Store) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// SpoolBytes returns total bytes used by the spool.
func (s *Store) SpoolBytes() uint64 {
	var total uint64
	filepath.Walk(s.root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

// RetentionResult reports what retention enforcement removed.
type RetentionResult struct {
	DeletedSegments int
	DeletedBytes    int64
}

// EnforceRetention deletes oldest closed segments until the spool fits under
// maxBytes, and any segment older than maxAgeDays. The active segments are
// never touched.
func (s *Store) EnforceRetention(maxBytes int64, maxAgeDays int) (RetentionResult, error) {
	var res RetentionResult
	segs, err := s.ListClosed()
	if err != nil {
		return res, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays)
	total := int64(s.SpoolBytes())
	for _, seg := range segs {
		tooOld := maxAgeDays > 0 && !seg.MaxTS.IsZero() && seg.MaxTS.Before(cutoff)
		overCap := maxBytes > 0 && total > maxBytes
		if !tooOld && !overCap {
			continue
		}
		if err := s.Remove(seg.Name); err == nil {
			res.DeletedSegments++
			res.DeletedBytes += seg.SizeBytes
			total -= seg.SizeBytes
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// ReadSegmentFile decodes every complete record from a (possibly torn)
// segment file. Returns the records, whether the tail was truncated, and a
// hard error only if the file cannot be opened at all.
func ReadSegmentFile(path string) ([]json.RawMessage, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return ReadSegment(f)
}

// ReadSegment decodes concatenated gzip members of NDJSON records.
func ReadSegment(r io.Reader) ([]json.RawMessage, bool, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		if err == io.EOF { // empty file
			return nil, false, nil
		}
		return nil, true, nil // header torn: no complete records
	}
	zr.Multistream(true)
	var out []json.RawMessage
	truncated := false
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			truncated = true
			continue
		}
		out = append(out, json.RawMessage(append([]byte(nil), line...)))
	}
	if sc.Err() != nil {
		truncated = true // torn final member: keep everything before it
	}
	return out, truncated, nil
}
