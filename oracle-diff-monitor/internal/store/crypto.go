package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func deriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

func getSecretKey() []byte {
	secret := os.Getenv("ODM_SECRET")
	if secret == "" {
		secret = "oracle-diff-monitor-default-secret-key-2024"
	}
	return deriveKey(secret)
}

func encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key := getSecretKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func decrypt(hexCiphertext string) (string, error) {
	if hexCiphertext == "" {
		return "", nil
	}
	key := getSecretKey()
	data, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return hexCiphertext, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return hexCiphertext, nil
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return hexCiphertext, nil
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return hexCiphertext, nil
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return hexCiphertext, nil
	}
	return string(plaintext), nil
}
