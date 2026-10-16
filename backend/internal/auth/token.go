// Package auth issues and verifies compact signed session tokens.
//
// A token is base64url(payload) + "." + base64url(HMAC-SHA256(payload)). It is
// intentionally small and dependency-free; the payload carries just enough to
// identify a user without a database round trip on every WebSocket upgrade.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Errors returned by Verify.
var (
	ErrMalformed = errors.New("auth: malformed token")
	ErrSignature = errors.New("auth: bad signature")
	ErrExpired   = errors.New("auth: token expired")
)

// Claims is the signed payload.
type Claims struct {
	UserID string `json:"uid"`
	Name   string `json:"name"`
	Exp    int64  `json:"exp"` // unix seconds
}

// Signer creates and verifies tokens with a shared secret.
type Signer struct {
	secret []byte
	now    func() time.Time
}

// NewSigner returns a Signer. The secret should be at least 32 random bytes.
func NewSigner(secret string) *Signer {
	return &Signer{secret: []byte(secret), now: time.Now}
}

var enc = base64.RawURLEncoding

func (s *Signer) mac(payload string) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// Issue signs claims that expire after ttl.
func (s *Signer) Issue(userID, name string, ttl time.Duration) (string, Claims, error) {
	c := Claims{UserID: userID, Name: name, Exp: s.now().Add(ttl).Unix()}
	body, err := json.Marshal(c)
	if err != nil {
		return "", c, err
	}
	payload := enc.EncodeToString(body)
	return payload + "." + enc.EncodeToString(s.mac(payload)), c, nil
}

// Verify checks signature and expiry and returns the claims.
func (s *Signer) Verify(token string) (Claims, error) {
	var c Claims
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return c, ErrMalformed
	}
	got, err := enc.DecodeString(sig)
	if err != nil {
		return c, ErrMalformed
	}
	if !hmac.Equal(got, s.mac(payload)) {
		return c, ErrSignature
	}
	body, err := enc.DecodeString(payload)
	if err != nil {
		return c, ErrMalformed
	}
	if err := json.Unmarshal(body, &c); err != nil || c.UserID == "" {
		return Claims{}, ErrMalformed
	}
	if s.now().Unix() >= c.Exp {
		return Claims{}, ErrExpired
	}
	return c, nil
}
