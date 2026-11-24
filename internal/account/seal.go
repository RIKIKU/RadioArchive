package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// sealer seals what the API keeps in a browser's cookie while a sign-in is
// under way (its state, nonce, PKCE verifier and return path) with
// AES-256-GCM, so the server stores nothing of a listener who hasn't
// finished signing in (P4) and the browser can neither read nor change it.
// The cookie's name is bound in as additional data, so a value sealed for
// one use can't stand in for another's. (The session itself is a row of
// the database since ADR 0022, and its cookie only a random ID.)
type sealer struct{ aead cipher.AEAD }

// cookieKeyInfo names the key's use in HKDF. Changing it only fails the
// sign-ins under way at that moment.
const cookieKeyInfo = "radioarchive listener cookies v1"

// newSealer derives the cookies' key from secret, the OIDC client secret
// (ADR 0009): a key of its own would be one more secret for the operator
// to keep, and HKDF keeps the two uses apart. Rotating the client secret
// fails only the sign-ins under way; sessions are rows, and stay.
func newSealer(secret []byte) (*sealer, error) {
	if len(secret) < 16 {
		return nil, errors.New("the OIDC client secret is too short to derive a cookie key from (at least 16 bytes)")
	}
	key, err := hkdf.Key(sha256.New, secret, nil, cookieKeyInfo, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(name string, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, []byte(name))), nil
}

var errUnsealed = errors.New("cookie doesn't open")

func (s *sealer) open(name, value string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(b) < s.aead.NonceSize() {
		return errUnsealed
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, b[:n], b[n:], []byte(name))
	if err != nil {
		return errUnsealed
	}
	if json.Unmarshal(plain, v) != nil {
		return errUnsealed
	}
	return nil
}
