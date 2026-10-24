// Package secrets encrypts user-supplied provider API keys at rest.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Box seals and opens values with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// Load builds a Box from SECRET_KEY (any string, hashed to 32 bytes) or, when it's
// empty, from a random key persisted at keyFile (created with 0600 on first run).
// Losing that file makes stored provider keys unreadable, so it lives next to the DB.
func Load(envKey, keyFile string) (*Box, error) {
	var key []byte
	if envKey != "" {
		sum := sha256.Sum256([]byte(envKey))
		key = sum[:]
	} else {
		raw, err := os.ReadFile(keyFile)
		switch {
		case err == nil:
			key, err = hex.DecodeString(strings.TrimSpace(string(raw)))
			if err != nil || len(key) != 32 {
				return nil, fmt.Errorf("secret key file %s is corrupt", keyFile)
			}
		case errors.Is(err, os.ErrNotExist):
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(keyFile), 0755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(key)), 0600); err != nil {
				return nil, fmt.Errorf("could not persist secret key: %w", err)
			}
		default:
			return nil, err
		}
	}

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

func (b *Box) Seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (b *Box) Open(sealed []byte) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("ciphertext too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", errors.New("could not decrypt key (was SECRET_KEY changed?)")
	}
	return string(plain), nil
}
