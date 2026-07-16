package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := New(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestProviderSettingsAreEncryptedAtRest(t *testing.T) {
	store := newTestStore(t)

	if err := store.SaveProviderSetting(ProviderSetting{
		Provider:  "openai",
		APIKey:    "sk-test-secret",
		IsEnabled: true,
	}); err != nil {
		t.Fatalf("SaveProviderSetting() error = %v", err)
	}

	var rawValue string
	if err := store.db.QueryRow(`SELECT api_key FROM provider_settings WHERE provider = ?`, "openai").Scan(&rawValue); err != nil {
		t.Fatalf("raw query error = %v", err)
	}
	if rawValue == "sk-test-secret" || rawValue == "" {
		t.Fatalf("api_key stored without encryption: %q", rawValue)
	}

	setting, err := store.GetProviderSetting("openai")
	if err != nil {
		t.Fatalf("GetProviderSetting() error = %v", err)
	}
	if setting.APIKey != "sk-test-secret" {
		t.Fatalf("GetProviderSetting().APIKey = %q, want original value", setting.APIKey)
	}
}

func TestSessionsAreStoredHashed(t *testing.T) {
	store := newTestStore(t)

	token, err := store.CreateSession(time.Hour)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	var storedToken string
	if err := store.db.QueryRow(`SELECT token FROM sessions LIMIT 1`).Scan(&storedToken); err != nil {
		t.Fatalf("session query error = %v", err)
	}
	if storedToken == token {
		t.Fatalf("session token stored in plaintext")
	}
	if !store.ValidateSession(token) {
		t.Fatalf("ValidateSession() returned false for a valid token")
	}
}

func TestLoginLockoutAndSuccessfulReset(t *testing.T) {
	store := newTestStore(t)
	const ip = "192.0.2.10"

	for i := 0; i < 5; i++ {
		if err := store.RecordLoginAttempt(ip, false); err != nil {
			t.Fatalf("RecordLoginAttempt(false) error = %v", err)
		}
	}
	if !store.IsIPLocked(ip) {
		t.Fatal("IsIPLocked() = false after five failures")
	}
	remaining := store.GetLockoutRemaining(ip)
	if remaining <= 0 || remaining > 15*60 {
		t.Fatalf("GetLockoutRemaining() = %d", remaining)
	}

	if err := store.RecordLoginAttempt(ip, true); err != nil {
		t.Fatalf("RecordLoginAttempt(true) error = %v", err)
	}
	if store.IsIPLocked(ip) {
		t.Fatal("successful login did not reset lockout")
	}

	if err := store.RecordLoginAttempt(ip, false); err != nil {
		t.Fatalf("RecordLoginAttempt(false) after reset error = %v", err)
	}
	if store.IsIPLocked(ip) {
		t.Fatal("one failure after a successful login caused a lockout")
	}
}

func TestRecordLoginAttemptRemovesExpiredRows(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.db.Exec(`
		INSERT INTO login_attempts (ip, attempted_at, success)
		VALUES ('198.51.100.1', datetime('now', '-2 days'), 0)
	`); err != nil {
		t.Fatalf("insert expired attempt: %v", err)
	}

	if err := store.RecordLoginAttempt("192.0.2.20", false); err != nil {
		t.Fatalf("RecordLoginAttempt() error = %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE ip = '198.51.100.1'`).Scan(&count); err != nil {
		t.Fatalf("count expired attempts: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired attempt count = %d, want 0", count)
	}
}

func TestProviderSettingsLifecycle(t *testing.T) {
	store := newTestStore(t)

	settings := []ProviderSetting{
		{Provider: "openai", APIKey: "openai-secret", IsEnabled: true},
		{Provider: "ollama", BaseURL: "http://localhost:11434", IsEnabled: true},
		{Provider: "disabled", APIKey: "disabled-secret", IsEnabled: false},
	}
	for _, setting := range settings {
		if err := store.SaveProviderSetting(setting); err != nil {
			t.Fatalf("SaveProviderSetting(%q) error = %v", setting.Provider, err)
		}
	}

	all, err := store.GetAllProviderSettings()
	if err != nil {
		t.Fatalf("GetAllProviderSettings() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("setting count = %d", len(all))
	}
	if got := store.GetProviderAPIKey("openai"); got != "openai-secret" {
		t.Fatalf("GetProviderAPIKey(openai) = %q", got)
	}
	if got := store.GetProviderAPIKey("disabled"); got != "" {
		t.Fatalf("GetProviderAPIKey(disabled) = %q", got)
	}

	ollama, err := store.GetProviderSetting("ollama")
	if err != nil {
		t.Fatalf("GetProviderSetting(ollama) error = %v", err)
	}
	if ollama == nil || ollama.BaseURL != "http://localhost:11434" || !ollama.IsEnabled {
		t.Fatalf("unexpected Ollama setting: %#v", ollama)
	}

	if err := store.DeleteProviderSetting("openai"); err != nil {
		t.Fatalf("DeleteProviderSetting() error = %v", err)
	}
	deleted, err := store.GetProviderSetting("openai")
	if err != nil {
		t.Fatalf("GetProviderSetting(deleted) error = %v", err)
	}
	if deleted != nil {
		t.Fatalf("deleted setting = %#v", deleted)
	}
}

func TestRequestAnalyticsAggregates(t *testing.T) {
	store := newTestStore(t)

	logs := []RequestLog{
		{
			Model:            "gpt-test",
			Provider:         "openai",
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			LatencyMs:        100,
			StatusCode:       200,
			ClientIP:         "192.0.2.1",
		},
		{
			Model:            "gpt-test",
			Provider:         "openai",
			PromptTokens:     4,
			CompletionTokens: 2,
			TotalTokens:      6,
			LatencyMs:        300,
			StatusCode:       502,
			ErrorMessage:     "upstream failed",
			ClientIP:         "192.0.2.2",
		},
	}
	for _, entry := range logs {
		if err := store.LogRequest(entry); err != nil {
			t.Fatalf("LogRequest() error = %v", err)
		}
	}

	stats, err := store.GetStats(time.Hour)
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}
	if stats.TotalRequests != 2 || stats.TotalTokens != 21 || stats.ErrorCount != 1 ||
		stats.AvgLatencyMs != 200 || stats.ModelBreakdown["gpt-test"] != 2 ||
		stats.ProviderBreakdown["openai"] != 2 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	recent, err := store.GetRecentLogs(10)
	if err != nil {
		t.Fatalf("GetRecentLogs() error = %v", err)
	}
	if len(recent) != 2 || recent[0].StatusCode != 502 || recent[1].StatusCode != 200 {
		t.Fatalf("unexpected recent logs: %#v", recent)
	}

	daily, err := store.GetDailyStats(1)
	if err != nil {
		t.Fatalf("GetDailyStats() error = %v", err)
	}
	if len(daily) != 1 || daily[0].Requests != 2 || daily[0].Tokens != 21 || daily[0].Errors != 1 {
		t.Fatalf("unexpected daily stats: %#v", daily)
	}

	monthly, err := store.GetMonthlyStats(1)
	if err != nil {
		t.Fatalf("GetMonthlyStats() error = %v", err)
	}
	if len(monthly) != 1 || monthly[0].Requests != 2 || monthly[0].Tokens != 21 {
		t.Fatalf("unexpected monthly stats: %#v", monthly)
	}

	providers, err := store.GetProviderPeriodStats(1)
	if err != nil {
		t.Fatalf("GetProviderPeriodStats() error = %v", err)
	}
	if len(providers) != 1 || providers[0].Provider != "openai" || providers[0].Tokens != 21 {
		t.Fatalf("unexpected provider stats: %#v", providers)
	}

	models, err := store.GetModelCostStats(1)
	if err != nil {
		t.Fatalf("GetModelCostStats() error = %v", err)
	}
	if len(models) != 1 || models[0].Model != "gpt-test" || models[0].PromptTokens != 14 ||
		models[0].CompletionTokens != 7 || models[0].TotalTokens != 21 {
		t.Fatalf("unexpected model stats: %#v", models)
	}
}

func TestSessionAndPasswordLifecycle(t *testing.T) {
	store := newTestStore(t)

	if err := store.SetAdminPassword("hash"); err != nil {
		t.Fatalf("SetAdminPassword() error = %v", err)
	}
	if hash, err := store.GetAdminPasswordHash(); err != nil || hash != "hash" {
		t.Fatalf("GetAdminPasswordHash() = %q, %v", hash, err)
	}

	validToken, err := store.CreateSession(time.Hour)
	if err != nil {
		t.Fatalf("CreateSession(valid) error = %v", err)
	}
	expiredToken, err := store.CreateSession(-time.Hour)
	if err != nil {
		t.Fatalf("CreateSession(expired) error = %v", err)
	}
	if !store.ValidateSession(validToken) || store.ValidateSession(expiredToken) {
		t.Fatal("unexpected session validity")
	}
	if err := store.CleanExpiredSessions(); err != nil {
		t.Fatalf("CleanExpiredSessions() error = %v", err)
	}
	if err := store.DeleteSession(validToken); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if store.ValidateSession(validToken) {
		t.Fatal("DeleteSession() left session valid")
	}

	resetToken, err := store.CreateSession(time.Hour)
	if err != nil {
		t.Fatalf("CreateSession(reset) error = %v", err)
	}
	if err := store.ResetAdminPassword(); err != nil {
		t.Fatalf("ResetAdminPassword() error = %v", err)
	}
	if store.HasAdminPassword() || store.ValidateSession(resetToken) {
		t.Fatal("ResetAdminPassword() did not clear password and sessions")
	}
}
