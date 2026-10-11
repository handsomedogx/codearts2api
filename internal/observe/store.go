package observe

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type UsageEvent struct {
	Time             string  `json:"time"`
	AccountID        string  `json:"account_id"`
	Environment      string  `json:"environment"`
	Model            string  `json:"model"`
	Success          bool    `json:"success"`
	ErrorCategory    string  `json:"error_category"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	LatencyMS        float64 `json:"latency_ms"`
}

type RequestEntry struct {
	Time             string  `json:"time"`
	TS               float64 `json:"ts"`
	RequestID        string  `json:"request_id"`
	Method           string  `json:"method"`
	Path             string  `json:"path"`
	Status           int     `json:"status"`
	Success          bool    `json:"success"`
	Stream           bool    `json:"stream"`
	Model            string  `json:"model"`
	AccountID        string  `json:"account_id"`
	AccountName      string  `json:"account_name"`
	Environment      string  `json:"environment"`
	SourceIP         string  `json:"source_ip"`
	UserAgent        string  `json:"user_agent"`
	DurationMS       float64 `json:"duration_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Attempts         int     `json:"attempts"`
	ConversationID   string  `json:"conversation_id"`
	ErrorCategory    string  `json:"error_category"`
}

type Store struct {
	UsagePath string
	LogsPath  string
	mu        sync.Mutex
	active    atomic.Int64
}

func New(usagePath, logsPath string) *Store {
	return &Store{UsagePath: usagePath, LogsPath: logsPath}
}

func (s *Store) Begin() time.Time {
	s.active.Add(1)
	return time.Now()
}

func (s *Store) Finish(started time.Time, entry RequestEntry) {
	entry.DurationMS = math.Max(0, float64(time.Since(started).Microseconds())/1000)
	if entry.Time == "" {
		entry.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.TS == 0 {
		entry.TS = float64(time.Now().UnixNano()) / 1e9
	}
	if entry.Method == "" {
		entry.Method = "POST"
	}
	if entry.Path == "" {
		entry.Path = "/v1/chat/completions"
	}
	entry.RequestID = trim(entry.RequestID, 96)
	entry.Model = trim(entry.Model, 160)
	entry.AccountID = trim(entry.AccountID, 128)
	entry.AccountName = trim(entry.AccountName, 160)
	entry.Environment = trim(entry.Environment, 32)
	entry.SourceIP = trim(entry.SourceIP, 128)
	entry.UserAgent = trim(entry.UserAgent, 768)
	entry.ConversationID = trim(entry.ConversationID, 128)
	entry.ErrorCategory = trim(entry.ErrorCategory, 96)
	if entry.TotalTokens <= 0 {
		entry.TotalTokens = entry.PromptTokens + entry.CompletionTokens
	}
	_ = s.appendJSONL(s.LogsPath, entry)
	s.active.Add(-1)
}

func (s *Store) AppendUsage(event UsageEvent) {
	if event.Time == "" {
		event.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if event.PromptTokens < 0 {
		event.PromptTokens = 0
	}
	if event.CompletionTokens < 0 {
		event.CompletionTokens = 0
	}
	if event.TotalTokens <= 0 {
		event.TotalTokens = event.PromptTokens + event.CompletionTokens
	}
	if event.LatencyMS < 0 {
		event.LatencyMS = 0
	}
	event.ErrorCategory = trim(event.ErrorCategory, 80)
	_ = s.appendJSONL(s.UsagePath, event)
}

func (s *Store) appendJSONL(path string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = file.Write(encoded)
	return err
}

func (s *Store) UsageSummary(days, points int) map[string]any {
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	if points < 6 {
		points = 6
	}
	if points > 96 {
		points = 96
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	events := make([]UsageEvent, 0)
	_ = scanJSONL(s.UsagePath, func(raw []byte) {
		var item UsageEvent
		if json.Unmarshal(raw, &item) != nil {
			return
		}
		when, err := time.Parse(time.RFC3339, item.Time)
		if err != nil || when.Before(cutoff) {
			return
		}
		events = append(events, item)
	})
	requestCount, failures := len(events), 0
	prompt, completion, total := 0, 0, 0
	totalLatency := 0.0
	for _, item := range events {
		if !item.Success {
			failures++
		}
		prompt += maxInt(0, item.PromptTokens)
		completion += maxInt(0, item.CompletionTokens)
		value := item.TotalTokens
		if value <= 0 {
			value = item.PromptTokens + item.CompletionTokens
		}
		total += maxInt(0, value)
		totalLatency += math.Max(0, item.LatencyMS)
	}
	span := now.Sub(cutoff).Seconds()
	if span < 1 {
		span = 1
	}
	bucketSeconds := span / float64(points)
	buckets := make([]map[string]any, points)
	for i := range buckets {
		buckets[i] = map[string]any{"start": cutoff.Add(time.Duration(float64(i)*bucketSeconds) * time.Second).Format(time.RFC3339), "prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0, "requests": 0, "failures": 0, "latency_ms": 0.0}
	}
	for _, item := range events {
		when, _ := time.Parse(time.RFC3339, item.Time)
		idx := int(math.Floor(when.Sub(cutoff).Seconds() / bucketSeconds))
		if idx < 0 {
			idx = 0
		}
		if idx >= points {
			idx = points - 1
		}
		b := buckets[idx]
		b["prompt_tokens"] = b["prompt_tokens"].(int) + maxInt(0, item.PromptTokens)
		b["completion_tokens"] = b["completion_tokens"].(int) + maxInt(0, item.CompletionTokens)
		value := item.TotalTokens
		if value <= 0 {
			value = item.PromptTokens + item.CompletionTokens
		}
		b["total_tokens"] = b["total_tokens"].(int) + maxInt(0, value)
		b["requests"] = b["requests"].(int) + 1
		if !item.Success {
			b["failures"] = b["failures"].(int) + 1
		}
		b["latency_ms"] = b["latency_ms"].(float64) + math.Max(0, item.LatencyMS)
	}
	peakTokens, peakAt, nonempty, sumNonempty := 0, "", 0, 0
	for _, b := range buckets {
		requests := b["requests"].(int)
		latency := b["latency_ms"].(float64)
		delete(b, "latency_ms")
		if requests > 0 {
			b["avg_latency_ms"] = round1(latency / float64(requests))
			nonempty++
			sumNonempty += b["total_tokens"].(int)
		} else {
			b["avg_latency_ms"] = 0.0
		}
		if b["total_tokens"].(int) > peakTokens {
			peakTokens = b["total_tokens"].(int)
			peakAt = b["start"].(string)
		}
	}
	successes := requestCount - failures
	avgLatency := 0.0
	if requestCount > 0 {
		avgLatency = totalLatency / float64(requestCount)
	}
	outputTPS := 0.0
	if totalLatency > 0 {
		outputTPS = float64(completion) / (totalLatency / 1000)
	}
	avgBucket := 0.0
	if nonempty > 0 {
		avgBucket = float64(sumNonempty) / float64(nonempty)
	}
	return map[string]any{"range_days": days, "points": points, "start": cutoff.Format(time.RFC3339), "end": now.Format(time.RFC3339), "request_count": requestCount, "success_count": successes, "failure_count": failures, "success_rate": percent(successes, requestCount, 100), "prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": total, "prompt_share": percent(prompt, total, 0), "completion_share": percent(completion, total, 0), "avg_latency_ms": round1(avgLatency), "avg_output_tps": round2(outputTPS), "peak_tokens": peakTokens, "peak_at": peakAt, "average_bucket_tokens": round1(avgBucket), "bucket_seconds": math.Round(bucketSeconds*1000) / 1000, "buckets": buckets}
}

func (s *Store) QueryLogs(q, result, timeRange string, limit int) map[string]any {
	q = strings.ToLower(strings.TrimSpace(q))
	result = strings.ToLower(strings.TrimSpace(result))
	if result != "success" && result != "failure" {
		result = "all"
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	duration := rangeDuration(timeRange)
	now := time.Now()
	rows := make([]RequestEntry, 0, limit)
	completed, successes, httpSuccesses := 0, 0, 0
	totalLatency := 0.0
	matched := 0
	_ = scanJSONL(s.LogsPath, func(raw []byte) {
		var row RequestEntry
		if json.Unmarshal(raw, &row) != nil {
			return
		}
		if duration > 0 && row.TS < float64(now.Add(-duration).UnixNano())/1e9 {
			return
		}
		blob := strings.ToLower(strings.Join([]string{row.RequestID, row.Model, row.AccountID, row.AccountName, row.Environment, row.SourceIP, row.UserAgent, row.ConversationID, row.ErrorCategory}, "\n"))
		if q != "" && !strings.Contains(blob, q) {
			return
		}
		completed++
		if row.Success {
			successes++
		}
		if row.Status >= 200 && row.Status < 300 {
			httpSuccesses++
		}
		totalLatency += math.Max(0, row.DurationMS)
		if result == "success" && !row.Success {
			return
		}
		if result == "failure" && row.Success {
			return
		}
		matched++
		rows = append(rows, row)
		if len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
	})
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TS > rows[j].TS })
	var successRate, httpRate, avg any
	if completed > 0 {
		successRate = math.Round(float64(successes)/float64(completed)*1000) / 10
		httpRate = math.Round(float64(httpSuccesses)/float64(completed)*1000) / 10
		avg = round1(totalLatency / float64(completed))
	}
	return map[string]any{"items": rows, "total_matched": matched, "summary": map[string]any{"completed": completed, "successes": successes, "failures": completed - successes, "success_rate": successRate, "http_success_rate": httpRate, "avg_latency_ms": avg, "in_progress": s.active.Load()}, "archive_size_bytes": fileSize(s.LogsPath), "filters": map[string]any{"q": q, "result": result, "time_range": timeRange, "limit": limit}}
}

func scanJSONL(path string, visit func([]byte)) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			visit([]byte(line))
		}
	}
	return scanner.Err()
}
func rangeDuration(value string) time.Duration {
	switch value {
	case "1h":
		return time.Hour
	case "24h", "":
		return 24 * time.Hour
	case "3d":
		return 72 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	case "all":
		return 0
	default:
		return 24 * time.Hour
	}
}
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
func trim(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func percent(a, b int, empty float64) float64 {
	if b == 0 {
		return empty
	}
	return math.Round(float64(a)/float64(b)*10000) / 100
}
func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
