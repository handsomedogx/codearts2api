package securestore

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func Available() bool { return available() }

func Prefix() string { return platformPrefix() }

func ProtectionName() string { return platformProtectionName() }

func PrepareRuntime(paths ...string) error { return prepareRuntime(paths...) }

func ProtectString(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	protected, err := protectBytes([]byte(value))
	if err != nil {
		return "", err
	}
	return Prefix() + base64.RawStdEncoding.EncodeToString(protected), nil
}

func UnprotectString(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, Prefix()) {
		return "", fmt.Errorf("value is not %s protected", ProtectionName())
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, Prefix()))
	if err != nil {
		return "", fmt.Errorf("decode protected value: %w", err)
	}
	plain, err := unprotectBytes(payload)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func IsProtected(value string) bool { return strings.HasPrefix(value, Prefix()) }

type Envelope struct {
	Version    int    `json:"version"`
	Protection string `json:"protection"`
	Protected  string `json:"protected"`
}

func MarshalProtectedJSON(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !Available() {
		return json.MarshalIndent(value, "", "  ")
	}
	protected, err := ProtectString(string(plain))
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(Envelope{Version: 1, Protection: ProtectionName(), Protected: protected}, "", "  ")
}

func UnmarshalProtectedJSON(raw []byte, target any) (bool, error) {
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Protected != "" {
		plain, err := UnprotectString(envelope.Protected)
		if err != nil {
			return true, err
		}
		if err := json.Unmarshal([]byte(plain), target); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, json.Unmarshal(raw, target)
}
