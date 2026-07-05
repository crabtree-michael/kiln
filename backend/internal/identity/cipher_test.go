package identity_test

import (
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// 32 bytes of hex — a valid master key shape (KILN_SECRETS_KEY, 11 §3 D7).
const testKey = "3f9c2b8a71d04e5f6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f50617"

func mustCipher(t *testing.T) *identity.Cipher {
	t.Helper()
	c, err := identity.NewCipher(testKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func mustEncrypt(t *testing.T, c *identity.Cipher, s string) []byte {
	t.Helper()
	box, err := c.Encrypt(s)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return box
}

func TestCipherRoundTrip(t *testing.T) {
	c, err := identity.NewCipher(testKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	box, err := c.Encrypt("sk-ant-secret-x4Kd")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(string(box), "sk-ant") {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := c.Decrypt(box)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "sk-ant-secret-x4Kd" {
		t.Fatalf("round trip = %q", got)
	}
}

func TestCipherDistinctNonces(t *testing.T) {
	c := mustCipher(t)
	a := mustEncrypt(t, c, "same")
	b := mustEncrypt(t, c, "same")
	if string(a) == string(b) {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestNewCipherRejectsBadKeys(t *testing.T) {
	for _, k := range []string{"", "abc", "zz" + testKey[2:], testKey[:32]} {
		if _, err := identity.NewCipher(k); err == nil {
			t.Fatalf("NewCipher(%q) accepted a malformed key", k)
		}
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	c := mustCipher(t)
	if _, err := c.Decrypt([]byte("short")); err == nil {
		t.Fatal("Decrypt accepted truncated ciphertext")
	}
	box := mustEncrypt(t, c, "secret")
	box[len(box)-1] ^= 0xFF
	if _, err := c.Decrypt(box); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}

func TestTail(t *testing.T) {
	if got := identity.Tail("sk-ant-secret-x4Kd"); got != "x4Kd" {
		t.Fatalf("Tail = %q", got)
	}
	if got := identity.Tail("ab"); got != "ab" {
		t.Fatalf("Tail short = %q", got)
	}
	if got := identity.Tail(""); got != "" {
		t.Fatalf("Tail empty = %q", got)
	}
}
