package securestore

import "testing"

func TestDPAPIRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("DPAPI is only available on Windows")
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
		t.Skip("DPAPI is only available on Windows")
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

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
