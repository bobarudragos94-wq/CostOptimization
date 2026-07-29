package hostid

import "testing"

func TestStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	id1, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := Load(dir)
	if err != nil || id1 != id2 {
		t.Fatalf("host id must be stable: %s vs %s (%v)", id1, id2, err)
	}
	if len(id1) != 36 || id1[:4] != "ura-" {
		t.Fatalf("format: %q", id1)
	}
	other, _ := Load(t.TempDir())
	if other == id1 {
		t.Fatal("distinct hosts must get distinct ids")
	}
}
