// Package bundle implements the encrypted export format (.urab) and its
// verification/import.
//
// A bundle is a plain tar archive containing:
//
//	manifest.json.age          age-encrypted model.Manifest
//	segments/<name>.age        age-encrypted gzip NDJSON segments
//
// Integrity model:
//   - every file is an independent age stream: per-chunk AEAD detects any
//     tampering or truncation of that file on decryption;
//   - the manifest lists the SHA-256 of every segment *ciphertext*, so a
//     swapped/substituted segment (even a validly-encrypted one from another
//     bundle) is detected against the manifest;
//   - one damaged segment is skipped and reported — the rest of the bundle
//     imports normally.
package bundle

import (
	"archive/tar"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/bobarudragos94-wq/costoptimization/internal/crypt"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/store"
)

const manifestName = "manifest.json.age"

// ExportInput carries everything needed to build a bundle.
type ExportInput struct {
	Store      *store.Store
	Recipient  string
	HostID     string
	Hostname   string
	Timezone   string
	ConfigHash string
	Health     model.HealthSummary
	OutDir     string
	// RemoveAfter deletes exported segments from the spool on success.
	RemoveAfter bool
}

// Export rotates the store, encrypts every closed segment and writes a single
// .urab file atomically. Returns the bundle path.
func Export(in ExportInput) (string, error) {
	if err := in.Store.Rotate(); err != nil {
		return "", fmt.Errorf("rotate: %w", err)
	}
	segs, err := in.Store.ListClosed()
	if err != nil {
		return "", err
	}
	if len(segs) == 0 {
		return "", fmt.Errorf("nothing to export")
	}
	if err := os.MkdirAll(in.OutDir, 0o750); err != nil {
		return "", err
	}
	// Remove crash orphans from previous interrupted exports: stale encrypt
	// temp dirs and half-written bundles must not accumulate or be mistaken
	// for complete exports.
	if orphans, err := filepath.Glob(filepath.Join(in.OutDir, "export-*")); err == nil {
		for _, o := range orphans {
			os.RemoveAll(o)
		}
	}
	if orphans, err := filepath.Glob(filepath.Join(in.OutDir, "*.urab.tmp")); err == nil {
		for _, o := range orphans {
			os.Remove(o)
		}
	}

	var bid [8]byte
	rand.Read(bid[:])
	bundleID := hex.EncodeToString(bid[:])
	now := time.Now().UTC()

	manifest := model.Manifest{
		SchemaVersion: model.SchemaVersion,
		AgentVersion:  model.AgentVersion,
		BundleID:      bundleID,
		HostID:        in.HostID,
		Hostname:      in.Hostname,
		Timezone:      in.Timezone,
		CreatedAt:     now,
		ConfigHash:    in.ConfigHash,
		Health:        in.Health,
	}

	outPath := filepath.Join(in.OutDir, fmt.Sprintf("%s-%s.urab", in.HostID, now.Format("20060102T150405Z")))
	tmpPath := outPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", err
	}
	cleanup := func() { f.Close(); os.Remove(tmpPath) }
	tw := tar.NewWriter(f)

	// Encrypt each segment into memory-bounded temp files first so the tar
	// header can carry the exact ciphertext size and hash.
	tmpDir, err := os.MkdirTemp(in.OutDir, "export-*")
	if err != nil {
		cleanup()
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	for _, seg := range segs {
		ct := filepath.Join(tmpDir, seg.Name+".age")
		if err := crypt.EncryptFile(in.Store.ClosedPath(seg.Name), ct, in.Recipient); err != nil {
			cleanup()
			return "", fmt.Errorf("encrypt %s: %w", seg.Name, err)
		}
		sum, size, err := sha256File(ct)
		if err != nil {
			cleanup()
			return "", err
		}
		name := "segments/" + seg.Name + ".age"
		manifest.Segments = append(manifest.Segments, model.SegmentMeta{
			Name: name, Kind: seg.Kind, SHA256: sum, SizeBytes: size,
			Records: seg.Records, MinTS: seg.MinTS, MaxTS: seg.MaxTS,
		})
		if manifest.PeriodStart.IsZero() || (!seg.MinTS.IsZero() && seg.MinTS.Before(manifest.PeriodStart)) {
			manifest.PeriodStart = seg.MinTS
		}
		if seg.MaxTS.After(manifest.PeriodEnd) {
			manifest.PeriodEnd = seg.MaxTS
		}
	}

	// Manifest first inside the tar so import can stream.
	mb, _ := json.MarshalIndent(manifest, "", " ")
	encManifest, err := encryptBytes(mb, in.Recipient)
	if err != nil {
		cleanup()
		return "", err
	}
	if err := writeTarFile(tw, manifestName, encManifest, now); err != nil {
		cleanup()
		return "", err
	}
	for _, sm := range manifest.Segments {
		b, err := os.ReadFile(filepath.Join(tmpDir, filepath.Base(sm.Name)))
		if err != nil {
			cleanup()
			return "", err
		}
		if err := writeTarFile(tw, sm.Name, b, now); err != nil {
			cleanup()
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	if in.RemoveAfter {
		for _, seg := range segs {
			in.Store.Remove(seg.Name)
		}
	}
	return outPath, nil
}

func encryptBytes(plain []byte, recipient string) ([]byte, error) {
	var buf strings.Builder
	_ = buf
	var out []byte
	w := &appendWriter{&out}
	ew, err := crypt.Encrypt(w, recipient)
	if err != nil {
		return nil, err
	}
	if _, err := ew.Write(plain); err != nil {
		return nil, err
	}
	if err := ew.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

type appendWriter struct{ b *[]byte }

func (w *appendWriter) Write(p []byte) (int, error) { *w.b = append(*w.b, p...); return len(p), nil }

func writeTarFile(tw *tar.Writer, name string, data []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o640, Size: int64(len(data)), ModTime: mod,
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ---------------------------------------------------------------------------
// Import
// ---------------------------------------------------------------------------

// SegmentResult is the outcome of importing one segment.
type SegmentResult struct {
	Meta    model.SegmentMeta
	Status  string // ok | hash_mismatch | decrypt_failed | truncated_records | missing
	Records []json.RawMessage
	Err     string
}

// ImportResult is the outcome of importing one bundle file.
type ImportResult struct {
	Path     string
	Manifest *model.Manifest
	Segments []SegmentResult
	// Fatal is set when the bundle cannot be used at all (unreadable tar,
	// missing/undecryptable manifest).
	Fatal string
}

// Ok reports whether at least one segment imported cleanly.
func (r *ImportResult) Ok() bool {
	if r.Fatal != "" {
		return false
	}
	for _, s := range r.Segments {
		if s.Status == "ok" || s.Status == "truncated_records" {
			return true
		}
	}
	return false
}

// Import reads one .urab file, decrypts and validates it. Damaged segments
// are reported individually and never abort the whole bundle.
func Import(path string, identities []age.Identity) *ImportResult {
	res := &ImportResult{Path: path}
	f, err := os.Open(path)
	if err != nil {
		res.Fatal = fmt.Sprintf("open: %v", err)
		return res
	}
	defer f.Close()

	// Pass 1: read every entry into memory-mapped temp map (bundle files are
	// modest: minute aggregates for 90 days compress to tens of MB).
	files := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Fatal = fmt.Sprintf("read tar: %v", err)
			// keep whatever entries were read before the corruption
			break
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if strings.Contains(name, "..") {
			continue // path traversal defense
		}
		b, err := io.ReadAll(io.LimitReader(tr, 1<<30))
		if err != nil {
			continue
		}
		files[name] = b
	}

	mb, ok := files[manifestName]
	if !ok {
		res.Fatal = "manifest missing"
		return res
	}
	plain, err := decryptBytes(mb, identities)
	if err != nil {
		res.Fatal = fmt.Sprintf("manifest decrypt failed (wrong key or tampered): %v", err)
		return res
	}
	var manifest model.Manifest
	if err := json.Unmarshal(plain, &manifest); err != nil {
		res.Fatal = fmt.Sprintf("manifest parse: %v", err)
		return res
	}
	res.Manifest = &manifest

	for _, sm := range manifest.Segments {
		sr := SegmentResult{Meta: sm}
		ct, ok := files[sm.Name]
		if !ok {
			sr.Status = "missing"
			sr.Err = "segment listed in manifest but absent from bundle"
			res.Segments = append(res.Segments, sr)
			continue
		}
		sum := sha256.Sum256(ct)
		if hex.EncodeToString(sum[:]) != sm.SHA256 {
			sr.Status = "hash_mismatch"
			sr.Err = "ciphertext hash does not match manifest (tampered or substituted)"
			res.Segments = append(res.Segments, sr)
			continue
		}
		seg, err := decryptBytes(ct, identities)
		if err != nil {
			sr.Status = "decrypt_failed"
			sr.Err = fmt.Sprintf("authenticated decryption failed: %v", err)
			res.Segments = append(res.Segments, sr)
			continue
		}
		recs, truncated, _ := store.ReadSegment(strings.NewReader(string(seg)))
		sr.Records = recs
		if truncated {
			sr.Status = "truncated_records"
			sr.Err = "segment contained a torn tail (agent crash); complete records recovered"
		} else {
			sr.Status = "ok"
		}
		res.Segments = append(res.Segments, sr)
	}
	return res
}

func decryptBytes(ct []byte, identities []age.Identity) ([]byte, error) {
	r, err := crypt.Decrypt(strings.NewReader(string(ct)), identities)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
