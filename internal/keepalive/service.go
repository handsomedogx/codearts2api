package keepalive

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
)

type Service struct {
	Settings config.Settings
	Auth     *auth.Manager
	Client   *codearts.Client

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(settings config.Settings, manager *auth.Manager, client *codearts.Client) *Service {
	return &Service{Settings: settings, Auth: manager, Client: client}
}

func (s *Service) Start() {
	if !s.Settings.KeepaliveEnabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		poll := time.Duration(maxInt(5, s.Settings.KeepalivePollSeconds)) * time.Second
		timer := time.NewTimer(poll)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				_, _ = s.RunOnce(ctx, true)
				timer.Reset(poll)
			}
		}
	}()
}

func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

func (s *Service) RunOnce(ctx context.Context, dueOnly bool) ([]auth.PublicAccount, error) {
	if !s.Settings.KeepaliveEnabled {
		return []auth.PublicAccount{}, nil
	}
	accounts, err := s.Auth.ListAccounts()
	if err != nil {
		return nil, err
	}
	results := make([]auth.PublicAccount, 0, len(accounts))
	processed := 0
	for _, account := range accounts {
		record, err := s.Auth.LoadAccount(account.ID)
		if err != nil {
			continue
		}
		if dueOnly && !s.due(record) {
			continue
		}
		if processed > 0 && s.Settings.KeepaliveAccountGapSeconds > 0 {
			gap := time.Duration(s.Settings.KeepaliveAccountGapSeconds * float64(time.Second))
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(gap):
			}
		}
		processed++
		results = append(results, s.ProbeAccount(ctx, record))
	}
	return results, nil
}

func (s *Service) due(record auth.AccountRecord) bool {
	if record.Health == nil {
		return true
	}
	status, _ := record.Health["probe_status"].(string)
	lastProbe, _ := record.Health["last_probe_at"].(string)
	if status != "ok" || lastProbe == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, lastProbe)
	if err != nil {
		return true
	}
	interval := maxInt(60, s.Settings.KeepaliveIntervalSeconds)
	return time.Since(parsed) >= time.Duration(interval)*time.Second
}

func (s *Service) ProbeAccount(ctx context.Context, record auth.AccountRecord) auth.PublicAccount {
	accountID := record.ID
	if record.AccessKeyID == "" || record.SecretAccessKey == "" {
		_ = s.Client.Pool.MarkFailure(accountID, "", "auth_probe", http.StatusUnauthorized, 0)
		_ = s.Client.Pool.RecordProbe(accountID, false, "credentials_unavailable", http.StatusUnauthorized, true)
		return s.public(accountID)
	}

	probe := func() error {
		profile, err := s.Settings.Profile(record.Environment)
		if err != nil {
			return err
		}
		var last error
		for attempt := 0; attempt < 2; attempt++ {
			_, err = s.Client.GetJSON(ctx, accountID, profile.BaseURL+"/v1/model/builtin", "PromptCenter", nil)
			if err == nil {
				return nil
			}
			last = err
			upstream, ok := err.(*codearts.UpstreamError)
			if !ok || upstream.Category != "network" || attempt > 0 {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		return last
	}

	err := probe()
	if err != nil {
		upstream, isUpstream := err.(*codearts.UpstreamError)
		authRejected := isUpstream && (upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden || upstream.Category == "auth")
		if authRejected && record.RefreshToken != "" && len(record.AuthContext) > 0 {
			if _, refreshErr := s.Auth.RefreshAccount(accountID); refreshErr == nil {
				_ = s.Client.Pool.RecordRefreshProbe(accountID, true, "")
				record, _ = s.Auth.LoadAccount(accountID)
				err = probe()
			} else {
				category, terminal := refreshCategory(refreshErr)
				_ = s.Client.Pool.RecordRefreshProbe(accountID, false, category)
				if terminal {
					_ = s.Client.Pool.MarkFailure(accountID, "", "auth_probe", http.StatusUnauthorized, 0)
				}
				_ = s.Client.Pool.RecordProbe(accountID, false, category, chooseStatus(terminal, http.StatusUnauthorized), terminal)
				return s.public(accountID)
			}
		}
		if err != nil {
			status, category, needsRelogin := 0, "probe_error", false
			if upstream, ok := err.(*codearts.UpstreamError); ok {
				status, category = upstream.StatusCode, upstream.Category
				needsRelogin = status == http.StatusUnauthorized || status == http.StatusForbidden || category == "auth"
				if needsRelogin {
					_ = s.Client.Pool.MarkFailure(accountID, "", "auth_probe", status, 0)
				}
			} else if ctx.Err() != nil {
				category = "network"
			}
			_ = s.Client.Pool.RecordProbe(accountID, false, category, status, needsRelogin)
			return s.public(accountID)
		}
	}

	_ = s.Client.Pool.RecordProbe(accountID, true, "", 0, false)
	current, _ := s.Auth.LoadAccount(accountID)
	if current.RefreshToken != "" && len(current.AuthContext) > 0 {
		if _, refreshErr := s.Auth.RefreshAccount(accountID); refreshErr != nil {
			category, _ := refreshCategory(refreshErr)
			_ = s.Client.Pool.RecordRefreshProbe(accountID, false, category)
		} else {
			_ = s.Client.Pool.RecordRefreshProbe(accountID, true, "")
		}
	}
	return s.public(accountID)
}

func (s *Service) public(accountID string) auth.PublicAccount {
	accounts, _ := s.Client.Pool.ListPublic()
	for _, account := range accounts {
		if account.ID == accountID {
			return account
		}
	}
	return auth.PublicAccount{ID: accountID}
}

func refreshCategory(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"invalid_grant", "offline user session not found", "refresh token failed"} {
		if strings.Contains(text, marker) {
			return "auth_probe", true
		}
	}
	if strings.Contains(text, "transport") || strings.Contains(text, "timeout") || strings.Contains(text, "connection") {
		return "network", false
	}
	return "refresh_error", false
}

func chooseStatus(condition bool, value int) int {
	if condition {
		return value
	}
	return 0
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
