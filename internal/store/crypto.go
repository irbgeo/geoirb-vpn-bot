package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// sealedPrefix marks an encrypted value and its format version.
const sealedPrefix = "v1:"

// sealer encrypts secrets at rest with AES-256-GCM. The associated data
// binds a ciphertext to one record: moved to another record, it won't open.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(
	key []byte,
) (*sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("store: secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("store: secret key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: secret key: %w", err)
	}
	return &sealer{
		aead: aead,
	}, nil
}

// seal returns "v1:" + base64(nonce | ciphertext) of in.Text.
func (s *sealer) seal(in sealInput) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	_, err := rand.Read(nonce)
	if err != nil {
		return "", fmt.Errorf("store: nonce: %w", err)
	}
	out := s.aead.Seal(nonce, nonce, []byte(in.Text), []byte(in.AAD))
	return sealedPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// open reverses seal (in.Text is the sealed value). It fails on a wrong
// key, wrong AAD or tampering.
func (s *sealer) open(in sealInput) (string, error) {
	body, ok := strings.CutPrefix(in.Text, sealedPrefix)
	if !ok {
		return "", errors.New("store: value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("store: decode sealed value: %w", err)
	}
	n := s.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("store: sealed value too short")
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(in.AAD))
	if err != nil {
		return "", fmt.Errorf("store: decrypt: %w", err)
	}
	return string(plain), nil
}
