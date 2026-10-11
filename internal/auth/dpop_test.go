package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGeneratePKCE(t *testing.T) {
	pair, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.CodeVerifier) != 128 {
		t.Fatalf("expected 128-char verifier, got %d", len(pair.CodeVerifier))
	}
	if pair.CodeChallengeMethod != "SHA-256" {
		t.Fatalf("unexpected method %q", pair.CodeChallengeMethod)
	}
}

func TestSignDPoPShape(t *testing.T) {
	pair, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	token, err := SignDPoP(pair, "post", "https://example.com/token", time.Unix(1234567890, 0), "fixed-jti")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with 3 parts, got %d", len(parts))
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["htm"] != "POST" || payload["htu"] != "https://example.com/token" || payload["jti"] != "fixed-jti" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
