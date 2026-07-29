// Package e2e validates the complete offline workflow:
// synthetic fleet → encrypted bundles → import → consolidated reports,
// plus tamper handling and analyzer behavior on damaged inputs.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobarudragos94-wq/costoptimization/internal/analyzer"
	"github.com/bobarudragos94-wq/costoptimization/internal/crypt"
	"github.com/bobarudragos94-wq/costoptimization/internal/synth"
)

func TestFullWorkflow(t *testing.T) {
	dir := t.TempDir()
	id, rec, err := crypt.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "identity.txt")
	os.WriteFile(keyFile, []byte(id+"\n"), 0o600)

	bundles := filepath.Join(dir, "bundles")
	paths, err := synth.GenerateFleet(rec, bundles, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 6 {
		t.Fatalf("want 6 bundles, got %d", len(paths))
	}

	ids, _ := crypt.LoadIdentities(keyFile)
	out := filepath.Join(dir, "reports")
	res, err := analyzer.Run(bundles, ids, out)
	if err != nil {
		t.Fatal(err)
	}
	if res.OKBundles != 6 || res.RejectedBundles != 0 {
		t.Fatalf("import: %+v", res)
	}
	if res.Hosts != 6 || res.SQLInstances != 3 {
		t.Fatalf("consolidation: %+v", res)
	}
	if res.Spikes < 10 {
		t.Fatalf("expected recurring nightly spikes, got %d", res.Spikes)
	}

	// The three report artifacts must exist and be non-trivial.
	for _, f := range []string{"report.json", "report.md", "report.html"} {
		st, err := os.Stat(filepath.Join(out, f))
		if err != nil || st.Size() < 1000 {
			t.Fatalf("report %s missing or too small: %v", f, err)
		}
	}

	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	report := string(md)
	// Every designed classification must appear in the human-readable report.
	for _, want := range []string{
		"likely_rightsizing_candidate",
		"optimize_recurring_workload_before_rightsizing",
		"sql_memory_configuration_issue",
		"ha_dr_constraint",
		"insufficient_os_headroom",
		"UNLIMITED DEFAULT",
		"AG-CORE",
		"validation required",
		"SQL Agent job",
		"recurring",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report.md missing expected content %q", want)
		}
	}
	// The secondary replica must never get a rightsizing suggestion.
	if strings.Contains(report, "sqlag-win-03") {
		idx := strings.Index(report, "sqlag-win-03")
		section := report[idx:min(idx+2500, len(report))]
		if strings.Contains(section, "suggested vCPU range") {
			t.Error("HA secondary must not receive a downsizing suggestion")
		}
	}
}

func TestTamperedBundleMarked(t *testing.T) {
	dir := t.TempDir()
	id, rec, _ := crypt.GenerateIdentity()
	keyFile := filepath.Join(dir, "identity.txt")
	os.WriteFile(keyFile, []byte(id+"\n"), 0o600)
	bundles := filepath.Join(dir, "bundles")
	paths, err := synth.GenerateFleet(rec, bundles, 5)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt one bundle mid-file.
	b, _ := os.ReadFile(paths[0])
	b[len(b)/2] ^= 0xFF
	os.WriteFile(paths[0], b, 0o640)

	ids, _ := crypt.LoadIdentities(keyFile)
	res, err := analyzer.Run(bundles, ids, filepath.Join(dir, "reports"))
	if err != nil {
		t.Fatal(err)
	}
	if res.PartialBundles+res.RejectedBundles == 0 {
		t.Fatalf("tampered bundle must be marked partial or rejected: %+v", res)
	}
	if res.Hosts < 5 {
		t.Fatalf("undamaged bundles must still be analyzed (got %d hosts)", res.Hosts)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("tampering must surface as a warning")
	}
}

func TestSchemaCompatibilityGolden(t *testing.T) {
	// A bundle produced by the current code must import with the current
	// schema version stamped on every record; this guards the golden format.
	dir := t.TempDir()
	id, rec, _ := crypt.GenerateIdentity()
	os.WriteFile(filepath.Join(dir, "id.txt"), []byte(id+"\n"), 0o600)
	paths, err := synth.GenerateFleet(rec, filepath.Join(dir, "b"), 3)
	if err != nil || len(paths) == 0 {
		t.Fatal(err)
	}
	ids, _ := crypt.LoadIdentities(filepath.Join(dir, "id.txt"))
	ds, err := analyzer.LoadBundles(filepath.Join(dir, "b"), ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range ds.Hosts {
		if h.Inventory == nil || h.Inventory.Schema == "" {
			t.Fatal("inventory record must carry schema version")
		}
		if len(h.Minutes) == 0 {
			t.Fatal("minutes missing")
		}
		if h.Minutes[0].Schema != h.Inventory.Schema {
			t.Fatal("schema version inconsistent across kinds")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
