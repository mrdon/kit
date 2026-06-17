package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Encryptor provides AES-256-GCM encryption and decryption.
type Encryptor struct {
	gcm cipher.AEAD
}

// NewEncryptor creates a new Encryptor from a hex-encoded 32-byte key.
func NewEncryptor(hexKey string) (*Encryptor, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decoding key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	return &Encryptor{gcm: gcm}, nil
}

// EncryptBytes encrypts plaintext bytes and returns nonce||ciphertext as
// raw bytes — suitable for a BYTEA column. The string Encrypt is the
// hex-encoded form of this.
func (e *Encryptor) EncryptBytes(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return e.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptBytes reverses EncryptBytes.
func (e *Encryptor) DecryptBytes(ciphertext []byte) ([]byte, error) {
	nonceSize := e.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	return plaintext, nil
}

// Encrypt encrypts plaintext and returns hex-encoded ciphertext.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	ciphertext, err := e.EncryptBytes([]byte(plaintext))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts hex-encoded ciphertext and returns the plaintext.
func (e *Encryptor) Decrypt(hexCiphertext string) (string, error) {
	ciphertext, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", fmt.Errorf("decoding ciphertext: %w", err)
	}
	plaintext, err := e.DecryptBytes(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
