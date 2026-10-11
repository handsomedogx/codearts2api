package signer

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignRequest(t *testing.T) {
	now := time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC)
	signed, err := SignRequest(
		"GET",
		"https://example.com/v5/caller-identity?b=2&a=1",
		http.Header{"Content-Type": {"application/json"}},
		nil,
		"ak",
		"sk",
		"token",
		"domain",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(signed.CanonicalRequest, "a=1&b=2") {
		t.Fatalf("canonical query not sorted: %s", signed.CanonicalRequest)
	}
	if !strings.HasPrefix(signed.Headers.Get("Authorization"), Algorithm+" Access=ak") {
		t.Fatalf("unexpected authorization: %s", signed.Headers.Get("Authorization"))
	}
}
