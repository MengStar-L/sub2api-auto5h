package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const keySize = 32

var ErrInvalidKey = errors.New("master key must be exactly 32 bytes")

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext []byte, aad string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := b.aead.Seal(nil, nonce, plaintext, []byte(aad))
	payload := append(nonce, sealed...)
	return base64.RawStdEncoding.EncodeToString(payload), nil
}

func (b *Box) Open(encoded, aad string) ([]byte, error) {
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := b.aead.NonceSize()
	if len(payload) < nonceSize+b.aead.Overhead() {
		return nil, errors.New("ciphertext is truncated")
	}
	plaintext, err := b.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("decrypt ciphertext: %w", err)
	}
	return plaintext, nil
}

func GenerateKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	return key, nil
}

func GenerateToken(bytes int) (plain string, hash string, err error) {
	value := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(value)
	return plain, HashToken(plain), nil
}
