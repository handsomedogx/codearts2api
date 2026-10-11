package observe

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUsageSummaryAndLogs(t *testing.T) {
	temp := t.TempDir()
	store := New(filepath.Join(temp, "usage.jsonl"), filepath.Join(temp, "logs.jsonl"))
	store.AppendUsage(UsageEvent{AccountID: "a1", Environment: "cn", Model: "GLM-5.2", Success: true, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, LatencyMS: 100})
	store.AppendUsage(UsageEvent{AccountID: "a2", Environment: "cn", Model: "GLM-5.2", Success: false, ErrorCategory: "server", LatencyMS: 50})

	summary := store.UsageSummary(3, 6)
	if summary["request_count"] != 2 || summary["success_count"] != 1 || summary["failure_count"] != 1 {
		t.Fatalf("unexpected usage summary: %#v", summary)
	}
	if summary["total_tokens"] != 15 {
		t.Fatalf("unexpected total tokens: %#v", summary["total_tokens"])
	}

	started := store.Begin()
	time.Sleep(time.Millisecond)
	store.Finish(started, RequestEntry{RequestID: "req-1", Status: 200, Success: true, Model: "GLM-5.2", AccountID: "a1", PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	logs := store.QueryLogs("GLM", "success", "24h", 100)
	if logs["total_matched"] != 1 {
		t.Fatalf("unexpected log query: %#v", logs)
	}
}
