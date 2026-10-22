// Package vault seals payer credentials with AES-256-GCM before they reach
// the database. The key comes from the environment (PAYAHUB_VAULT_KEY) and
// never touches the database, so a database dump alone reveals no secret.
// A managed KMS can replace the key source later without changing callers.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// version prefixes every sealed value so the key can be rotated later.
const version byte = 1

type Vault struct{ aead cipher.AEAD }

// New takes a base64-encoded 32-byte key.
func New(keyB64 string) (*Vault, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, errors.New("vault key must be 32 bytes, base64-encoded")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// NewKey returns a fresh random key in the form New expects.
func NewKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// Seal encrypts credentials. aad binds the result to its owner (organisation,
// payer, environment), so a sealed value copied to another row will not open.
func (v *Vault) Seal(creds map[string]string, aad string) ([]byte, error) {
	plain, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{version}, nonce...)
	return v.aead.Seal(out, nonce, plain, []byte(aad)), nil
}

// Owner is the additional data that binds payer credentials to their row.
func Owner(orgID, payerID, env string) string { return orgID + "/" + payerID + "/" + env }

func (v *Vault) Open(sealed []byte, aad string) (map[string]string, error) {
	n := v.aead.NonceSize()
	if len(sealed) < 1+n || sealed[0] != version {
		return nil, errors.New("vault: unknown sealed format")
	}
	plain, err := v.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	var creds map[string]string
	return creds, json.Unmarshal(plain, &creds)
}
