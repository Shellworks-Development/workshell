// Package secure provides encryption at rest for stored secrets (webhook URLs,
// GitHub tokens, signing secrets) and HMAC helpers.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "enc:v1:"

// Box encrypts and decrypts values with AES-256-GCM under a master key.
type Box struct {
	aead cipher.AEAD
}

// LoadKey returns the master key: from env (hex or base64, 32 bytes), or a
// key file in the data dir, generated with mode 0600 on first run.
func LoadKey(envValue, dataDir string) ([]byte, error) {
	if envValue != "" {
		if k, err := hex.DecodeString(envValue); err == nil && len(k) == 32 {
			return k, nil
		}
		if k, err := base64.StdEncoding.DecodeString(envValue); err == nil && len(k) == 32 {
			return k, nil
		}
		return nil, errors.New("WORKSHELL_SECRET_KEY must be 32 bytes, hex or base64 encoded")
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%s is corrupt", path)
		}
		return k, nil
	}
	k := make([]byte, 32)
	rand.Read(k)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return k, nil
}

func NewBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. Empty stays empty so "unset" is cheap to check.
func (b *Box) Seal(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(out)
}

// Open decrypts a value from Seal. Unprefixed values are returned as-is, so
// data written before encryption was enabled keeps working.
func (b *Box) Open(value string) (string, error) {
	if !strings.HasPrefix(value, prefix) {
		return value, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(value[len(prefix):])
	if err != nil {
		return "", err
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("ciphertext too short")
	}
	pt, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("cannot decrypt (was WORKSHELL_SECRET_KEY changed?)")
	}
	return string(pt), nil
}

// MustOpen is Open that returns "" on failure.
func (b *Box) MustOpen(value string) string {
	v, _ := b.Open(value)
	return v
}

// HMACHex returns hex(HMAC-SHA256(key, msg)).
func HMACHex(key, msg []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// HashID maps an id to a stable pseudonymous positive integer (53 bits, so it
// survives JSON/JavaScript numbers) using HMAC-SHA256 under key.
func HashID(key []byte, id int64) int64 {
	m := hmac.New(sha256.New, key)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(id))
	m.Write(b[:])
	sum := m.Sum(nil)
	v := int64(binary.BigEndian.Uint64(sum[:8]) >> 11)
	if v == 0 {
		v = 1
	}
	return v
}

// RandomToken returns n random bytes as hex.
func RandomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
