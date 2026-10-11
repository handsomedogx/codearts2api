package codearts

import "testing"

func TestToolCallFragmentsAreAccumulated(t *testing.T) {
	parts := map[int]map[string]any{}
	mergeToolCallParts(parts, []any{map[string]any{"index": 0.0, "id": "call-1", "type": "function", "function": map[string]any{"name": "wea", "arguments": "{\"c"}}})
	mergeToolCallParts(parts, []any{map[string]any{"index": 0.0, "function": map[string]any{"name": "ther", "arguments": "ity\":\"SZ\"}"}}})
	calls := toolCallsFromParts(parts)
	if len(calls) != 1 {
		t.Fatalf("unexpected calls: %#v", calls)
	}
	call := calls[0].(map[string]any)
	function := call["function"].(map[string]any)
	if call["id"] != "call-1" || call["type"] != "function" || function["name"] != "weather" || function["arguments"] != "{\"city\":\"SZ\"}" {
		t.Fatalf("unexpected merged tool call: %#v", call)
	}
}

func TestUpstreamEventError(t *testing.T) {
	err := upstreamEventError(map[string]any{"error": map[string]any{"code": "429", "message": "service busy"}})
	upstream, ok := err.(*UpstreamError)
	if !ok || upstream.Category != "upstream_event" || !upstream.Retryable {
		t.Fatalf("unexpected event error: %#v", err)
	}
}
