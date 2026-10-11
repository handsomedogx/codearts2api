package subscription

import "testing"

func TestParseSubscription(t *testing.T) {
	payload := map[string]any{
		"package":       map[string]any{"package_name_en": "Pro", "status": "active", "spec_code": "spec-1"},
		"metrics":       []any{map[string]any{"name": "usageTokenChatMessages", "usage_token_num": 125.0, "package_token_amount": 100.0}},
		"model_metrics": []any{map[string]any{"model_id": "GLM-5.2", "model_type": "chat", "usage_token_num": 50.0, "model_quota_num": 100.0, "enable": true, "status": "ok"}},
		"start_date":    "2026-10-01",
		"end_date":      "2026-10-31",
	}
	value := parse(payload, "a1", "cn")
	if value["available"] != true || value["used_tokens"] != 125 || value["quota_tokens"] != 100 {
		t.Fatalf("unexpected subscription: %#v", value)
	}
	if value["over_limit"] != true || value["overage_tokens"] != 25 {
		t.Fatalf("unexpected overage: %#v", value)
	}
	if value["package_name"] != "Pro" {
		t.Fatalf("unexpected package: %#v", value)
	}
}
