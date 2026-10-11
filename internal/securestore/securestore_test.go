package securestore

import (
	"bytes"
	"testing"
)

func TestPlatformProtectionRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("platform secure storage is unavailable")
	}
	plain := "sk-ca-test-secret-value"
	protected, err := ProtectString(plain)
	if err != nil {
		t.Fatal(err)
	}
	if protected == plain || !IsProtected(protected) {
		t.Fatalf("value was not protected: %q", protected)
	}
	decoded, err := UnprotectString(protected)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != plain {
		t.Fatalf("round trip mismatch: got %q", decoded)
	}
}

func TestProtectedJSONRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("platform secure storage is unavailable")
	}
	input := map[string]any{"refresh_token": "refresh-secret", "count": float64(1)}
	raw, err := MarshalProtectedJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || contains(string(raw), "refresh-secret") {
		t.Fatalf("plaintext leaked into protected JSON: %s", string(raw))
	}
	var output map[string]any
	protected, err := UnmarshalProtectedJSON(raw, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !protected || output["refresh_token"] != "refresh-secret" {
		t.Fatalf("unexpected output: %#v", output)
	}
}

func TestAESGCMRoundTripAndTamperDetection(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plain := []byte("linux-master-key-secret")
	sealed, err := sealAESGCM(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("AES-GCM payload contains plaintext")
	}
	opened, err := openAESGCM(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plain) {
		t.Fatalf("AES-GCM round trip mismatch: %q", opened)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := openAESGCM(key, tampered); err == nil {
		t.Fatal("tampered AES-GCM payload should fail authentication")
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
