// Package crypt wraps filippo.io/age (X25519 + ChaCha20-Poly1305 streaming
// AEAD) for all bundle encryption. No custom cryptography is implemented here.
//
// Key model:
//   - `ura-analyzer keygen` on the analyzer machine produces an identity
//     (private) and a recipient (public) string.
//   - Agents are configured with the recipient only and can therefore encrypt
//     but never decrypt — compromising a monitored server yields no telemetry.
//   - age's STREAM construction authenticates every 64 KiB chunk, so both
//     tampering and truncation of any segment fail decryption loudly.
//   - Rotation: generate a new identity, add the new recipient to agent
//     configs; both identities can be offered at import during the overlap.
package crypt

import (
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

// GenerateIdentity returns (identityString, recipientString).
func GenerateIdentity() (string, string, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}

// ParseRecipient validates an age recipient (public key) string.
func ParseRecipient(s string) (age.Recipient, error) {
	r, err := age.ParseX25519Recipient(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid age recipient (expected age1...): %w", err)
	}
	return r, nil
}

// LoadIdentities reads one or more age identities from a key file
// (lines starting with AGE-SECRET-KEY-; comments with # allowed).
func LoadIdentities(path string) ([]age.Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ids []age.Identity
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, fmt.Errorf("invalid identity in %s: %w", path, err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no identities found in %s", path)
	}
	return ids, nil
}

// Encrypt returns a WriteCloser that encrypts to dst for the recipient.
// Close must be called to flush the final authenticated chunk.
func Encrypt(dst io.Writer, recipient string) (io.WriteCloser, error) {
	r, err := ParseRecipient(recipient)
	if err != nil {
		return nil, err
	}
	return age.Encrypt(dst, r)
}

// Decrypt returns a reader of the plaintext. Any tampering or truncation
// surfaces as a read error before unauthenticated data is returned.
func Decrypt(src io.Reader, identities []age.Identity) (io.Reader, error) {
	return age.Decrypt(src, identities...)
}

// EncryptFile encrypts src into dst atomically (tmp + rename).
func EncryptFile(src, dst, recipient string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	w, err := Encrypt(out, recipient)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := w.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
