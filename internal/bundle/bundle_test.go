package bundle

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/bobarudragos94-wq/costoptimization/internal/crypt"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/store"
)

type testRec struct {
	model.Meta
	V int `json:"v"`
}

func makeBundle(t *testing.T, recipient string) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 2, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		s.Append(model.KindHostMinute, testRec{model.NewMeta(model.KindHostMinute, base.Add(time.Duration(i)*time.Minute)), i})
	}
	s.Append(model.KindSpike, testRec{model.NewMeta(model.KindSpike, base.Add(30 * time.Minute)), 999})
	out := t.TempDir()
	path, err := Export(ExportInput{
		Store: s, Recipient: recipient, HostID: "ura-test", Hostname: "h1",
		Timezone: "UTC", ConfigHash: "deadbeef", OutDir: out,
		Health: model.HealthSummary{ExpectedMinutes: 50, CollectedMinutes: 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, s
}

func TestExportImportRoundtrip(t *testing.T) {
	idStr, recip, err := crypt.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := t.TempDir() + "/key.txt"
	os.WriteFile(keyFile, []byte(idStr+"\n"), 0o600)
	ids, err := crypt.LoadIdentities(keyFile)
	if err != nil {
		t.Fatal(err)
	}

	path, _ := makeBundle(t, recip)
	res := Import(path, ids)
	if !res.Ok() || res.Fatal != "" {
		t.Fatalf("import failed: %+v", res.Fatal)
	}
	if res.Manifest.HostID != "ura-test" || res.Manifest.SchemaVersion != model.SchemaVersion {
		t.Fatalf("bad manifest: %+v", res.Manifest)
	}
	var total int
	for _, seg := range res.Segments {
		if seg.Status != "ok" {
			t.Fatalf("segment %s status %s: %s", seg.Meta.Name, seg.Status, seg.Err)
		}
		total += len(seg.Records)
	}
	if total != 51 {
		t.Fatalf("want 51 records, got %d", total)
	}
	var m model.Meta
	json.Unmarshal(res.Segments[0].Records[0], &m)
	if m.Schema != model.SchemaVersion {
		t.Fatalf("schema tag missing on imported record")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	_, recip, _ := crypt.GenerateIdentity()
	otherID, _, _ := crypt.GenerateIdentity()
	keyFile := t.TempDir() + "/key.txt"
	os.WriteFile(keyFile, []byte(otherID+"\n"), 0o600)
	ids, _ := crypt.LoadIdentities(keyFile)

	path, _ := makeBundle(t, recip)
	res := Import(path, ids)
	if res.Ok() || res.Fatal == "" {
		t.Fatalf("import with wrong key must fail fatally, got %+v", res)
	}
}

func TestTamperDetection(t *testing.T) {
	idStr, recip, _ := crypt.GenerateIdentity()
	keyFile := t.TempDir() + "/key.txt"
	os.WriteFile(keyFile, []byte(idStr+"\n"), 0o600)
	ids, _ := crypt.LoadIdentities(keyFile)

	path, _ := makeBundle(t, recip)

	// Flip one byte near the end of the bundle (inside a segment ciphertext).
	b, _ := os.ReadFile(path)
	// tar has 1024 zero bytes of trailer; flip well before that.
	pos := len(b) - 2048
	b[pos] ^= 0xFF
	tampered := t.TempDir() + "/tampered.urab"
	os.WriteFile(tampered, b, 0o640)

	res := Import(tampered, ids)
	if res.Fatal != "" {
		// Manifest happened to be hit — acceptable: fatal rejection.
		return
	}
	bad := 0
	good := 0
	for _, seg := range res.Segments {
		switch seg.Status {
		case "hash_mismatch", "decrypt_failed", "missing":
			bad++
		case "ok":
			good++
		}
	}
	if bad == 0 {
		t.Fatalf("tampering was not detected: %+v", res.Segments)
	}
	if good == 0 {
		t.Fatalf("undamaged segments should still import (corruption isolation)")
	}
}

func TestTruncatedBundle(t *testing.T) {
	idStr, recip, _ := crypt.GenerateIdentity()
	keyFile := t.TempDir() + "/key.txt"
	os.WriteFile(keyFile, []byte(idStr+"\n"), 0o600)
	ids, _ := crypt.LoadIdentities(keyFile)

	path, _ := makeBundle(t, recip)
	b, _ := os.ReadFile(path)
	trunc := t.TempDir() + "/trunc.urab"
	os.WriteFile(trunc, b[:len(b)*3/4], 0o640) // cut the last quarter

	res := Import(trunc, ids)
	// Either fatal (manifest lost) or partial with missing/failed segments —
	// but never silently complete.
	if res.Fatal == "" {
		incomplete := false
		for _, seg := range res.Segments {
			if seg.Status != "ok" {
				incomplete = true
			}
		}
		if !incomplete {
			t.Fatal("truncation went undetected")
		}
	}
}

func TestExportRemovesSpoolWhenAsked(t *testing.T) {
	_, recip, _ := crypt.GenerateIdentity()
	dir := t.TempDir()
	s, _ := store.Open(dir)
	s.Append(model.KindHostMinute, testRec{model.NewMeta(model.KindHostMinute, time.Now().UTC()), 1})
	out := t.TempDir()
	_, err := Export(ExportInput{Store: s, Recipient: recip, HostID: "x", Hostname: "h",
		Timezone: "UTC", OutDir: out, RemoveAfter: true})
	if err != nil {
		t.Fatal(err)
	}
	segs, _ := s.ListClosed()
	if len(segs) != 0 {
		t.Fatalf("spool should be empty after export with RemoveAfter, got %d", len(segs))
	}
}
