package auth

import (
	"strings"
	"testing"
	"time"
)

func TestIssueVerify(t *testing.T) {
	s := NewSigner("0123456789abcdef0123456789abcdef")
	tok, _, err := s.Issue("u1", "Dewi", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok)
	if err != nil || c.UserID != "u1" || c.Name != "Dewi" {
		t.Fatalf("claims=%+v err=%v", c, err)
	}
}

func TestRejectsTamperedExpiredAndForeign(t *testing.T) {
	s := NewSigner("0123456789abcdef0123456789abcdef")
	tok, _, _ := s.Issue("u1", "Dewi", time.Hour)

	parts := strings.Split(tok, ".")
	forged := enc.EncodeToString([]byte(`{"uid":"admin","name":"x","exp":9999999999}`)) + "." + parts[1]
	if _, err := s.Verify(forged); err != ErrSignature {
		t.Fatalf("forged payload: %v", err)
	}
	if _, err := NewSigner("another-secret-another-secret-123").Verify(tok); err != ErrSignature {
		t.Fatalf("foreign secret: %v", err)
	}
	for _, bad := range []string{"", "abc", ".", "a.", ".b", "a.b.c!"} {
		if _, err := s.Verify(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}

	past := NewSigner("0123456789abcdef0123456789abcdef")
	past.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	old, _, _ := past.Issue("u1", "Dewi", time.Hour)
	if _, err := s.Verify(old); err != ErrExpired {
		t.Fatalf("expired: %v", err)
	}
}
