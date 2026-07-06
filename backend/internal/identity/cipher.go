package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Cipher seals per-user secrets with AES-256-GCM under one master key
// (KILN_SECRETS_KEY — 11 §3, D7). Ciphertext layout: nonce || sealed.
type Cipher struct{ aead cipher.AEAD }

var (
	// ErrBadKey rejects a KILN_SECRETS_KEY that is not 64 hex chars (32 bytes).
	ErrBadKey = errors.New("identity: master key must be 64 hex chars (32 bytes)")
	// ErrBadCiphertext rejects truncated or tampered ciphertext.
	ErrBadCiphertext = errors.New("identity: ciphertext invalid")
)

// NewCipher parses the hex master key and prepares the AEAD.
func NewCipher(hexKey string) (*Cipher, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("identity: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("identity: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext under a fresh random nonce.
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("identity: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens nonce||sealed ciphertext produced by Encrypt.
func (c *Cipher) Decrypt(box []byte) (string, error) {
	if len(box) < c.aead.NonceSize() {
		return "", ErrBadCiphertext
	}
	nonce, sealed := box[:c.aead.NonceSize()], box[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrBadCiphertext
	}
	return string(plain), nil
}

// tailLen is the fingerprint length shown by the API (11 §3 D7).
const tailLen = 4

// Tail is the last-4 fingerprint shown by the API ("configured · …x4Kd"). A
// secret no longer than the fingerprint itself (≤4 chars) would have its tail
// BE the whole value, defeating the "fingerprint, never the value" contract
// (final review, Minor #4) — Tail reports "" for those instead of disclosing them.
func Tail(s string) string {
	if len(s) <= tailLen {
		return ""
	}
	return s[len(s)-tailLen:]
}
