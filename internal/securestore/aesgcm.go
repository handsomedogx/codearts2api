package securestore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

func sealAESGCM(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate AES-GCM nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return append(nonce, sealed...), nil
}

func openAESGCM(key, payload []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("AES-GCM payload is too short")
	}
	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt AES-GCM payload: %w", err)
	}
	return plain, nil
}
