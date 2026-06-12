package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// GenerateKey erzeugt ein neues Ed25519-Schlüsselpaar und liefert den privaten
// Seed (für CLIO_SIGNING_KEY) und den öffentlichen Schlüssel — jeweils
// base64-kodiert.
func GenerateKey() (seedB64, publicB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	seed := priv.Seed()
	return base64.StdEncoding.EncodeToString(seed),
		base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey liest einen base64-kodierten Ed25519-Schlüssel: entweder den
// 32-Byte-Seed oder den vollen 64-Byte-Privatschlüssel.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 dekodieren: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize: // 32
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize: // 64
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("ungültige schlüssellänge %d (erwartet %d oder %d bytes)",
			len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
	}
}

// EncodePublicKey kodiert einen öffentlichen Schlüssel base64.
func EncodePublicKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// signHash signiert den (hex-kodierten) Event-Hash und liefert die Signatur
// base64-kodiert.
func signHash(key ed25519.PrivateKey, hexHash string) (string, error) {
	digest, err := hex.DecodeString(hexHash)
	if err != nil {
		return "", fmt.Errorf("hash dekodieren: %w", err)
	}
	sig := ed25519.Sign(key, digest)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// verifySignature prüft eine base64-Signatur gegen den hex-kodierten Hash.
func verifySignature(pub ed25519.PublicKey, hexHash, sigB64 string) error {
	digest, err := hex.DecodeString(hexHash)
	if err != nil {
		return fmt.Errorf("hash dekodieren: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signatur dekodieren: %w", err)
	}
	if !ed25519.Verify(pub, digest, sig) {
		return errors.New("ed25519-prüfung fehlgeschlagen")
	}
	return nil
}
