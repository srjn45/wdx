// Package seal seals values under the hub's master key with AES-256-GCM, the
// additional data binding a sealed value to its row: one copied to another row
// does not open there.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the length of a key: 32 bytes, for AES-256.
const KeySize = 32

// ErrOpen is a sealed value that does not open: another key, other
// additional data, or bytes changed.
var ErrOpen = errors.New("the sealed value does not open")

// Seal returns plaintext sealed under key with additionalData: a random nonce
// followed by the ciphertext and its tag.
func Seal(key, plaintext, additionalData []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, additionalData), nil
}

// Open returns the plaintext of sealed, which Seal made under key with
// additionalData, or ErrOpen.
func Open(key, sealed, additionalData []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrOpen
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("a key is %d bytes, not %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
