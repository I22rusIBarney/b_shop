// Package secrets encrypts values stored in the database (payment keys, passwords)
// with AES-256-GCM using a key derived from SECRET_KEY.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "enc:v1:"

type Box struct {
	aead    cipher.AEAD
	signKey []byte
}

func New(secretKey string) (*Box, error) {
	encKey := sha256.Sum256([]byte("bshop/enc/" + secretKey))
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sign := sha256.Sum256([]byte("bshop/sign/" + secretKey))
	return &Box{aead: aead, signKey: sign[:]}, nil
}

// Encrypt returns an opaque string; empty input stays empty.
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" || IsEncrypted(plain) {
		return plain, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

func (b *Box) Decrypt(v string) (string, error) {
	if !IsEncrypted(v) {
		return v, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(v, prefix))
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("secrets: cannot decrypt (SECRET_KEY changed?)")
	}
	return string(plain), nil
}

// Sign returns an HMAC-SHA256 of msg, used for signed links and webhook secrets.
func (b *Box) Sign(msg string) string {
	m := hmac.New(sha256.New, b.signKey)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (b *Box) Verify(msg, sig string) bool {
	return hmac.Equal([]byte(b.Sign(msg)), []byte(sig))
}

func IsEncrypted(v string) bool { return strings.HasPrefix(v, prefix) }
