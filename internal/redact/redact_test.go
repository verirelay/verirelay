package redact

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestURLQueryStripsQueryAndFragment(t *testing.T) {
	for _, raw := range []string{
		"https://api.example.com/v1/messages?key=secret",
		"https://api.example.com/v1/messages?key=secret#frag",
		"https://api.example.com/v1/messages#frag",
	} {
		err := URLQuery(&url.Error{Op: "Post", URL: raw, Err: errors.New("connection refused")})
		if got := err.Error(); strings.ContainsAny(got, "?#") || !strings.Contains(got, "https://api.example.com/v1/messages") {
			t.Fatalf("URLQuery(%q) = %q", raw, got)
		}
	}
}

func TestURLQueryLeavesOtherErrors(t *testing.T) {
	plain := errors.New("plain?error")
	if got := URLQuery(plain); !errors.Is(got, plain) || got.Error() != "plain?error" {
		t.Fatalf("URLQuery(plain) = %v", got)
	}
	if got := URLQuery(nil); got != nil {
		t.Fatalf("URLQuery(nil) = %v", got)
	}
}
