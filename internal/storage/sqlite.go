package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db            *sql.DB
	encryptionKey []byte
}

type RequestLog struct {
	ID               int64
	Timestamp        time.Time
	Model            string
	Provider         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMs        int64
	StatusCode       int
	ErrorMessage     string
	ClientIP         string
}

// ProviderSetting represents a stored API key/URL for a provider.
type ProviderSetting struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url,omitempty"`
	IsEnabled bool   `json:"is_enabled"`
	UpdatedAt string `json:"updated_at"`
}

// PeriodStats represents aggregated stats for a time period.
type PeriodStats struct {
	Period   string  `json:"period"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Errors   int     `json:"errors"`
	AvgLatMs float64 `json:"avg_latency_ms"`
	// Token breakdowns for cost calc
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TopModel         string `json:"top_model,omitempty"`
}

// ProviderStats represents per-provider aggregated stats.
type ProviderPeriodStats struct {
	Period   string `json:"period"`
	Provider string `json:"provider"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
}

// ModelStats represents per-model aggregated stats.
type ModelCostStats struct {
	Model            string `json:"model"`
	Requests         int    `json:"requests"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
}

// Session represents an admin session.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	key, err := loadOrCreateEncryptionKey(dbPath)
	if err != nil {
		return nil, fmt.Errorf("load encryption key: %w", err)
	}

	return &Store{db: db, encryptionKey: key}, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS request_logs (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP,
		model           TEXT NOT NULL,
		provider        TEXT NOT NULL,
		prompt_tokens   INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0,
		total_tokens    INTEGER DEFAULT 0,
		latency_ms      INTEGER DEFAULT 0,
		status_code     INTEGER DEFAULT 200,
		error_message   TEXT DEFAULT '',
		client_ip       TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_request_logs_timestamp ON request_logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs(model);
	CREATE INDEX IF NOT EXISTS idx_request_logs_provider ON request_logs(provider);

	CREATE TABLE IF NOT EXISTS provider_settings (
		provider    TEXT PRIMARY KEY,
		api_key     TEXT DEFAULT '',
		base_url    TEXT DEFAULT '',
		is_enabled  INTEGER DEFAULT 0,
		updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS admin_config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token      TEXT PRIMARY KEY,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS login_attempts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		ip         TEXT NOT NULL,
		attempted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		success    INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_login_attempts_ip ON login_attempts(ip);
	`
	_, err := db.Exec(schema)
	return err
}

// ===================== Provider Settings =====================

func (s *Store) SaveProviderSetting(setting ProviderSetting) error {
	encryptedKey, err := s.encryptSensitive(setting.APIKey)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO provider_settings (provider, api_key, base_url, is_enabled, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider) DO UPDATE SET
			api_key = excluded.api_key,
			base_url = excluded.base_url,
			is_enabled = excluded.is_enabled,
			updated_at = CURRENT_TIMESTAMP`,
		setting.Provider, encryptedKey, setting.BaseURL, setting.IsEnabled,
	)
	return err
}

func (s *Store) GetProviderSetting(provider string) (*ProviderSetting, error) {
	var ps ProviderSetting
	err := s.db.QueryRow(`
		SELECT provider, api_key, base_url, is_enabled, updated_at
		FROM provider_settings WHERE provider = ?`, provider,
	).Scan(&ps.Provider, &ps.APIKey, &ps.BaseURL, &ps.IsEnabled, &ps.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ps.APIKey, err = s.decryptSensitive(ps.APIKey)
	if err != nil {
		return nil, err
	}
	return &ps, nil
}

func (s *Store) GetAllProviderSettings() ([]ProviderSetting, error) {
	rows, err := s.db.Query(`
		SELECT provider, api_key, base_url, is_enabled, updated_at
		FROM provider_settings ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var settings []ProviderSetting
	for rows.Next() {
		var ps ProviderSetting
		if err := rows.Scan(&ps.Provider, &ps.APIKey, &ps.BaseURL, &ps.IsEnabled, &ps.UpdatedAt); err != nil {
			return nil, err
		}
		ps.APIKey, err = s.decryptSensitive(ps.APIKey)
		if err != nil {
			return nil, err
		}
		settings = append(settings, ps)
	}
	return settings, nil
}

func (s *Store) DeleteProviderSetting(provider string) error {
	_, err := s.db.Exec(`DELETE FROM provider_settings WHERE provider = ?`, provider)
	return err
}

func (s *Store) GetProviderAPIKey(provider string) string {
	var encryptedKey string
	err := s.db.QueryRow(`SELECT api_key FROM provider_settings WHERE provider = ? AND is_enabled = 1`, provider).Scan(&encryptedKey)
	if err != nil {
		return ""
	}
	key, err := s.decryptSensitive(encryptedKey)
	if err != nil {
		return ""
	}
	return key
}

// ===================== Request Logging =====================

func (s *Store) LogRequest(log RequestLog) error {
	_, err := s.db.Exec(`
		INSERT INTO request_logs (model, provider, prompt_tokens, completion_tokens, total_tokens, latency_ms, status_code, error_message, client_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.Model, log.Provider, log.PromptTokens, log.CompletionTokens,
		log.TotalTokens, log.LatencyMs, log.StatusCode, log.ErrorMessage, log.ClientIP,
	)
	return err
}

// ===================== Dashboard Stats =====================

type Stats struct {
	TotalRequests     int            `json:"total_requests"`
	TotalTokens       int            `json:"total_tokens"`
	AvgLatencyMs      float64        `json:"avg_latency_ms"`
	ErrorCount        int            `json:"error_count"`
	ModelBreakdown    map[string]int `json:"model_breakdown"`
	ProviderBreakdown map[string]int `json:"provider_breakdown"`
}

func (s *Store) GetStats(since time.Duration) (*Stats, error) {
	cutoff := sqliteTimestamp(time.Now().Add(-since))

	stats := &Stats{
		ModelBreakdown:    make(map[string]int),
		ProviderBreakdown: make(map[string]int),
	}

	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(AVG(latency_ms), 0),
		       COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0)
		FROM request_logs WHERE timestamp >= ?`, cutoff,
	).Scan(&stats.TotalRequests, &stats.TotalTokens, &stats.AvgLatencyMs, &stats.ErrorCount)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT model, COUNT(*) FROM request_logs WHERE timestamp >= ? GROUP BY model`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		stats.ModelBreakdown[model] = count
	}

	rows2, err := s.db.Query(`
		SELECT provider, COUNT(*) FROM request_logs WHERE timestamp >= ? GROUP BY provider`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var provider string
		var count int
		if err := rows2.Scan(&provider, &count); err != nil {
			return nil, err
		}
		stats.ProviderBreakdown[provider] = count
	}

	return stats, nil
}

func (s *Store) GetRecentLogs(limit int) ([]RequestLog, error) {
	rows, err := s.db.Query(`
		SELECT id, timestamp, model, provider, prompt_tokens, completion_tokens,
		       total_tokens, latency_ms, status_code, error_message, client_ip
		FROM request_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []RequestLog
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.Model, &l.Provider,
			&l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
			&l.LatencyMs, &l.StatusCode, &l.ErrorMessage, &l.ClientIP); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// ===================== Analytics Queries =====================

// GetDailyStats returns per-day stats for the last N days.
func (s *Store) GetDailyStats(days int) ([]PeriodStats, error) {
	cutoff := sqliteTimestamp(time.Now().AddDate(0, 0, -days))
	rows, err := s.db.Query(`
		SELECT
			strftime('%Y-%m-%d', timestamp) AS period,
			COUNT(*) AS requests,
			COALESCE(SUM(total_tokens), 0) AS tokens,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0) AS errors,
			COALESCE(AVG(latency_ms), 0) AS avg_lat
		FROM request_logs
		WHERE timestamp >= ?
		GROUP BY period
		ORDER BY period`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PeriodStats
	for rows.Next() {
		var ps PeriodStats
		if err := rows.Scan(&ps.Period, &ps.Requests, &ps.Tokens,
			&ps.PromptTokens, &ps.CompletionTokens, &ps.Errors, &ps.AvgLatMs); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}

	// Add top model for each period
	for i, ps := range result {
		var topModel string
		err := s.db.QueryRow(`
			SELECT model FROM request_logs
			WHERE strftime('%Y-%m-%d', timestamp) = ?
			GROUP BY model ORDER BY COUNT(*) DESC LIMIT 1`, ps.Period).Scan(&topModel)
		if err == nil {
			result[i].TopModel = topModel
		}
	}

	return result, nil
}

// GetMonthlyStats returns per-month stats for the last N months.
func (s *Store) GetMonthlyStats(months int) ([]PeriodStats, error) {
	cutoff := sqliteTimestamp(time.Now().AddDate(0, -months, 0))
	rows, err := s.db.Query(`
		SELECT
			strftime('%Y-%m', timestamp) AS period,
			COUNT(*) AS requests,
			COALESCE(SUM(total_tokens), 0) AS tokens,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0) AS errors,
			COALESCE(AVG(latency_ms), 0) AS avg_lat
		FROM request_logs
		WHERE timestamp >= ?
		GROUP BY period
		ORDER BY period`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PeriodStats
	for rows.Next() {
		var ps PeriodStats
		if err := rows.Scan(&ps.Period, &ps.Requests, &ps.Tokens,
			&ps.PromptTokens, &ps.CompletionTokens, &ps.Errors, &ps.AvgLatMs); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}

	for i, ps := range result {
		var topModel string
		err := s.db.QueryRow(`
			SELECT model FROM request_logs
			WHERE strftime('%Y-%m', timestamp) = ?
			GROUP BY model ORDER BY COUNT(*) DESC LIMIT 1`, ps.Period).Scan(&topModel)
		if err == nil {
			result[i].TopModel = topModel
		}
	}

	return result, nil
}

// GetProviderPeriodStats returns per-provider breakdown by day for the last N days.
func (s *Store) GetProviderPeriodStats(days int) ([]ProviderPeriodStats, error) {
	cutoff := sqliteTimestamp(time.Now().AddDate(0, 0, -days))
	rows, err := s.db.Query(`
		SELECT
			strftime('%Y-%m-%d', timestamp) AS period,
			provider,
			COUNT(*) AS requests,
			COALESCE(SUM(total_tokens), 0) AS tokens
		FROM request_logs
		WHERE timestamp >= ?
		GROUP BY period, provider
		ORDER BY period, provider`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ProviderPeriodStats
	for rows.Next() {
		var ps ProviderPeriodStats
		if err := rows.Scan(&ps.Period, &ps.Provider, &ps.Requests, &ps.Tokens); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}
	return result, nil
}

// GetModelCostStats returns per-model token usage for cost calculation.
func (s *Store) GetModelCostStats(days int) ([]ModelCostStats, error) {
	cutoff := sqliteTimestamp(time.Now().AddDate(0, 0, -days))
	rows, err := s.db.Query(`
		SELECT
			model,
			COUNT(*) AS requests,
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_logs
		WHERE timestamp >= ?
		GROUP BY model
		ORDER BY SUM(total_tokens) DESC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelCostStats
	for rows.Next() {
		var ms ModelCostStats
		if err := rows.Scan(&ms.Model, &ms.Requests, &ms.PromptTokens,
			&ms.CompletionTokens, &ms.TotalTokens); err != nil {
			return nil, err
		}
		result = append(result, ms)
	}
	return result, nil
}

// ===================== Auth / Sessions =====================

// SetAdminPassword stores the bcrypt hash.
func (s *Store) SetAdminPassword(hash string) error {
	_, err := s.db.Exec(`
		INSERT INTO admin_config (key, value) VALUES ('password_hash', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, hash)
	return err
}

// GetAdminPasswordHash returns the stored bcrypt hash.
func (s *Store) GetAdminPasswordHash() (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT value FROM admin_config WHERE key = 'password_hash'`).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

// HasAdminPassword checks if a password has been set up.
func (s *Store) HasAdminPassword() bool {
	hash, err := s.GetAdminPasswordHash()
	return err == nil && hash != ""
}

// ResetAdminPassword removes the stored password.
func (s *Store) ResetAdminPassword() error {
	_, err := s.db.Exec(`DELETE FROM admin_config WHERE key = 'password_hash'`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM sessions`)
	return err
}

// CreateSession creates a new session token.
func (s *Store) CreateSession(duration time.Duration) (string, error) {
	// Generate 32-byte random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(duration)

	_, err := s.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, hashSessionToken(token), expiresAt)
	if err != nil {
		return "", err
	}

	return token, nil
}

// ValidateSession checks if a session token is valid and not expired.
func (s *Store) ValidateSession(token string) bool {
	var expiresAt time.Time
	err := s.db.QueryRow(`SELECT expires_at FROM sessions WHERE token IN (?, ?)`, hashSessionToken(token), token).Scan(&expiresAt)
	if err != nil {
		return false
	}
	return time.Now().Before(expiresAt)
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token IN (?, ?)`, hashSessionToken(token), token)
	return err
}

// CleanExpiredSessions removes expired sessions.
func (s *Store) CleanExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP`)
	return err
}

// RecordLoginAttempt records a login attempt.
func (s *Store) RecordLoginAttempt(ip string, success bool) error {
	if success {
		_, err := s.db.Exec(`DELETE FROM login_attempts WHERE ip = ?`, ip)
		return err
	}

	if _, err := s.db.Exec(`DELETE FROM login_attempts WHERE attempted_at < datetime('now', '-1 day')`); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO login_attempts (ip, success) VALUES (?, 0)`, ip)
	return err
}

// IsIPLocked checks if an IP has too many failed attempts in the last 15 minutes.
func (s *Store) IsIPLocked(ip string) bool {
	cutoffStr := sqliteTimestamp(time.Now().Add(-15 * time.Minute))
	var failCount int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM login_attempts
		WHERE ip = ? AND attempted_at >= ? AND success = 0`, ip, cutoffStr).Scan(&failCount)
	if err != nil {
		return false
	}
	return failCount >= 5
}

// GetLockoutRemaining returns seconds remaining on lockout for an IP.
func (s *Store) GetLockoutRemaining(ip string) int {
	cutoffStr := sqliteTimestamp(time.Now().Add(-15 * time.Minute))
	var lastAttemptStr string
	err := s.db.QueryRow(`
		SELECT MAX(attempted_at) FROM login_attempts
		WHERE ip = ? AND attempted_at >= ? AND success = 0`, ip, cutoffStr).Scan(&lastAttemptStr)
	if err != nil || lastAttemptStr == "" {
		return 0
	}
	lastAttempt, err := time.Parse("2006-01-02 15:04:05", lastAttemptStr)
	if err != nil {
		return 0
	}
	remaining := 15*60 - int(time.Since(lastAttempt).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *Store) Close() error {
	return s.db.Close()
}

func loadOrCreateEncryptionKey(dbPath string) ([]byte, error) {
	if secret := strings.TrimSpace(os.Getenv("LLM_GATEWAY_ENCRYPTION_KEY")); secret != "" {
		sum := sha256.Sum256([]byte(secret))
		return sum[:], nil
	}

	if dbPath == ":memory:" || strings.Contains(dbPath, "mode=memory") {
		return nil, nil
	}

	keyPath := dbPath + ".key"
	if data, err := os.ReadFile(keyPath); err == nil {
		return decodeEncryptionKey(strings.TrimSpace(string(data)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}

	return key, nil
}

func decodeEncryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}

	for _, decode := range []func(string) ([]byte, error){
		base64.RawStdEncoding.DecodeString,
		base64.StdEncoding.DecodeString,
		hex.DecodeString,
	} {
		decoded, err := decode(value)
		if err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}

	sum := sha256.Sum256([]byte(value))
	return sum[:], nil
}

func (s *Store) encryptSensitive(value string) (string, error) {
	if value == "" || len(s.encryptionKey) == 0 || strings.HasPrefix(value, "enc:") {
		return value, nil
	}

	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return "enc:" + base64.RawStdEncoding.EncodeToString(payload), nil
}

func (s *Store) decryptSensitive(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "enc:") {
		return value, nil
	}
	if len(s.encryptionKey) == 0 {
		return "", fmt.Errorf("encrypted secret present but no encryption key configured")
	}

	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, "enc:"))
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted secret is malformed")
	}

	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sqliteTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05")
}
