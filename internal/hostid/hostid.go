// Package hostid provides a stable, generated host identifier.
//
// The ID is random (not derived from hardware serials or MACs, which can be
// sensitive), generated once and persisted in the agent data directory so it
// survives restarts and re-exports. Reinstalling the agent with a wiped data
// directory yields a new ID; the analyzer keys everything on this ID plus
// hostname, so a customer can prove no cross-deployment tracking is possible.
package hostid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const fileName = "host.id"

// Load returns the persisted host ID, creating one on first run.
func Load(dataDir string) (string, error) {
	p := filepath.Join(dataDir, fileName)
	if b, err := os.ReadFile(p); err == nil {
		id := strings.TrimSpace(string(b))
		if strings.HasPrefix(id, "ura-") && len(id) == 4+32 {
			return id, nil
		}
		return "", fmt.Errorf("corrupt host id file %s: %q", p, id)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := "ura-" + hex.EncodeToString(raw[:])
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o640); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return id, nil
}
