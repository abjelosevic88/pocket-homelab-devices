package store

import (
	"regexp"
	"testing"
)

func TestDeviceToken(t *testing.T) {
	re := regexp.MustCompile(`^[2-9a-hjkmnp-z]{4}-[2-9a-hjkmnp-z]{4}-[2-9a-hjkmnp-z]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok := NewDeviceToken()
		if !re.MatchString(tok) {
			t.Fatalf("token %q is not xxxx-xxxx-xxxx", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q twice", tok)
		}
		seen[tok] = true
	}
	tok := "k7m4-x9qp-3hrt"
	for _, typed := range []string{"k7m4x9qp3hrt", "K7M4-X9QP-3HRT", " k7m4 x9qp 3hrt "} {
		if HashDeviceToken(typed) != HashDeviceToken(tok) {
			t.Fatalf("%q does not match %q", typed, tok)
		}
	}
	old := "0123456789abcdef0123456789abcdef01234567" // tokens made before 0.3.0
	if HashDeviceToken(old) != HashToken(old) {
		t.Fatal("an older hex token no longer matches")
	}
}
