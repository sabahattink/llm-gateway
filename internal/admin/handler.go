package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sabahattink/llm-gateway/internal/middleware"
	"github.com/sabahattink/llm-gateway/internal/storage"
)

const settingsBodyLimit = 64 << 10

// ProviderMeta describes a known provider for the settings UI.
type ProviderMeta struct {
	Name        string `json:"name"`
	EnvKey      string `json:"env_key"`
	Type        string `json:"type"` // "cloud" or "local"
	Placeholder string `json:"placeholder"`
}

// KnownProviders lists all providers the gateway can connect to.
var KnownProviders = []ProviderMeta{
	{Name: "anthropic", EnvKey: "ANTHROPIC_API_KEY", Type: "cloud", Placeholder: "sk-ant-..."},
	{Name: "openai", EnvKey: "OPENAI_API_KEY", Type: "cloud", Placeholder: "sk-..."},
	{Name: "google", EnvKey: "GOOGLE_API_KEY", Type: "cloud", Placeholder: "AIza..."},
	{Name: "groq", EnvKey: "GROQ_API_KEY", Type: "cloud", Placeholder: "gsk_..."},
	{Name: "mistral", EnvKey: "MISTRAL_API_KEY", Type: "cloud", Placeholder: "..."},
	{Name: "cohere", EnvKey: "COHERE_API_KEY", Type: "cloud", Placeholder: "..."},
	{Name: "xai", EnvKey: "XAI_API_KEY", Type: "cloud", Placeholder: "xai-..."},
	{Name: "perplexity", EnvKey: "PERPLEXITY_API_KEY", Type: "cloud", Placeholder: "pplx-..."},
	{Name: "together", EnvKey: "TOGETHER_API_KEY", Type: "cloud", Placeholder: "..."},
	{Name: "ollama", EnvKey: "OLLAMA_BASE_URL", Type: "local", Placeholder: "http://localhost:11434"},
	{Name: "lmstudio", EnvKey: "LMSTUDIO_BASE_URL", Type: "local", Placeholder: "http://localhost:1234"},
	{Name: "vllm", EnvKey: "VLLM_BASE_URL", Type: "local", Placeholder: "http://localhost:8000"},
}

// ReloadFunc is called after settings change so main.go can re-register providers.
type ReloadFunc func()

type Handler struct {
	store         *storage.Store
	dashboardHTML []byte
	settingsHTML  []byte
	analyticsHTML []byte
	onReload      ReloadFunc
}

func NewHandler(store *storage.Store, dashboardHTML, settingsHTML, analyticsHTML []byte, onReload ReloadFunc) *Handler {
	return &Handler{
		store:         store,
		dashboardHTML: dashboardHTML,
		settingsHTML:  settingsHTML,
		analyticsHTML: analyticsHTML,
		onReload:      onReload,
	}
}

// ServeDashboard serves the dashboard HTML page.
func (h *Handler) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(h.dashboardHTML)
}

// ServeSettings serves the settings HTML page.
func (h *Handler) ServeSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(h.settingsHTML)
}

// HandleDashboardData returns JSON data for the dashboard.
func (h *Handler) HandleDashboardData(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.GetStats(24 * time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logs, err := h.store.GetRecentLogs(50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stats":       stats,
		"recent_logs": logs,
		"cost_table":  middleware.CostPerMillionTokens,
	})
}

// HandleGetSettings returns all provider settings (API keys masked).
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stored, err := h.store.GetAllProviderSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build map of stored settings
	storedMap := make(map[string]storage.ProviderSetting)
	for _, s := range stored {
		storedMap[s.Provider] = s
	}

	// Build response with all known providers
	type settingResponse struct {
		Provider    string `json:"provider"`
		MaskedKey   string `json:"masked_key"`
		HasKey      bool   `json:"has_key"`
		BaseURL     string `json:"base_url"`
		IsEnabled   bool   `json:"is_enabled"`
		Type        string `json:"type"`
		Placeholder string `json:"placeholder"`
		EnvKey      string `json:"env_key"`
		UpdatedAt   string `json:"updated_at,omitempty"`
	}

	var response []settingResponse
	for _, meta := range KnownProviders {
		sr := settingResponse{
			Provider:    meta.Name,
			Type:        meta.Type,
			Placeholder: meta.Placeholder,
			EnvKey:      meta.EnvKey,
		}

		if s, ok := storedMap[meta.Name]; ok {
			sr.HasKey = s.APIKey != ""
			sr.MaskedKey = maskKey(s.APIKey)
			sr.BaseURL = s.BaseURL
			sr.IsEnabled = s.IsEnabled
			sr.UpdatedAt = s.UpdatedAt
		}

		response = append(response, sr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleSaveSetting saves a single provider's API key.
func (h *Handler) HandleSaveSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if err := readJSONBody(w, r, settingsBodyLimit, &req); err != nil {
		if isBodyTooLarge(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if req.Provider == "" {
		http.Error(w, "provider is required", http.StatusBadRequest)
		return
	}

	// Validate provider name
	valid := false
	for _, meta := range KnownProviders {
		if meta.Name == req.Provider {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	setting := storage.ProviderSetting{
		Provider:  req.Provider,
		APIKey:    strings.TrimSpace(req.APIKey),
		BaseURL:   strings.TrimSpace(req.BaseURL),
		IsEnabled: strings.TrimSpace(req.APIKey) != "" || strings.TrimSpace(req.BaseURL) != "",
	}

	if err := h.store.SaveProviderSetting(setting); err != nil {
		http.Error(w, "failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger provider re-registration
	if h.onReload != nil {
		h.onReload()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"provider": req.Provider,
		"message":  fmt.Sprintf("%s API key saved successfully", req.Provider),
	})
}

// HandleDeleteSetting removes a provider's API key.
func (h *Handler) HandleDeleteSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
	}
	if err := readJSONBody(w, r, settingsBodyLimit, &req); err != nil {
		if isBodyTooLarge(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if err := h.store.DeleteProviderSetting(req.Provider); err != nil {
		http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if h.onReload != nil {
		h.onReload()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"provider": req.Provider,
		"message":  fmt.Sprintf("%s API key removed", req.Provider),
	})
}

// HandleTestProvider tests connectivity to a provider.
func (h *Handler) HandleTestProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if err := readJSONBody(w, r, settingsBodyLimit, &req); err != nil {
		if isBodyTooLarge(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// If no key provided, try loading from store
	if req.APIKey == "" && req.BaseURL == "" {
		stored, _ := h.store.GetProviderSetting(req.Provider)
		if stored != nil {
			req.APIKey = stored.APIKey
			req.BaseURL = stored.BaseURL
		}
	}

	result := testProviderConnectivity(req.Provider, req.APIKey, req.BaseURL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// testProviderConnectivity pings a provider to check if the API key works.
func testProviderConnectivity(provider, apiKey, baseURL string) map[string]any {
	client := &http.Client{Timeout: 10 * time.Second}

	var testReq *http.Request
	var err error

	switch provider {
	case "anthropic":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		// Use models list endpoint to verify API key without spending tokens
		testReq, err = http.NewRequest("GET", "https://api.anthropic.com/v1/models", nil)
		testReq.Header.Set("x-api-key", apiKey)
		testReq.Header.Set("anthropic-version", "2023-06-01")

	case "openai":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "google":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey), nil)

	case "groq":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.groq.com/openai/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "mistral":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.mistral.ai/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "cohere":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.cohere.ai/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "xai":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.x.ai/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "perplexity":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("POST", "https://api.perplexity.ai/chat/completions", strings.NewReader(`{"model":"sonar-small","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`))
		testReq.Header.Set("Authorization", "Bearer "+apiKey)
		testReq.Header.Set("Content-Type", "application/json")

	case "together":
		if apiKey == "" {
			return map[string]any{"ok": false, "error": "no API key"}
		}
		testReq, err = http.NewRequest("GET", "https://api.together.xyz/v1/models", nil)
		testReq.Header.Set("Authorization", "Bearer "+apiKey)

	case "ollama":
		url := baseURL
		if url == "" {
			url = "http://localhost:11434"
		}
		testReq, err = http.NewRequest("GET", url+"/api/tags", nil)

	case "lmstudio":
		url := baseURL
		if url == "" {
			url = "http://localhost:1234"
		}
		testReq, err = http.NewRequest("GET", url+"/v1/models", nil)

	case "vllm":
		url := baseURL
		if url == "" {
			url = "http://localhost:8000"
		}
		testReq, err = http.NewRequest("GET", url+"/v1/models", nil)

	default:
		return map[string]any{"ok": false, "error": "unknown provider"}
	}

	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}

	start := time.Now()
	resp, err := client.Do(testReq)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "latency_ms": latency}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return map[string]any{"ok": true, "status": resp.StatusCode, "latency_ms": latency}
	}

	// Read error body for debugging
	errBody, _ := io.ReadAll(resp.Body)
	errMsg := string(errBody)
	if len(errMsg) > 200 {
		errMsg = errMsg[:200]
	}

	return map[string]any{
		"ok":         false,
		"status":     resp.StatusCode,
		"error":      errMsg,
		"latency_ms": latency,
	}
}

// ServeAnalytics serves the analytics HTML page.
func (h *Handler) ServeAnalytics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(h.analyticsHTML)
}

// HandleDailyStats returns daily aggregated stats.
func (h *Handler) HandleDailyStats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}
	if days > 365 {
		days = 365
	}

	stats, err := h.store.GetDailyStats(days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleMonthlyStats returns monthly aggregated stats.
func (h *Handler) HandleMonthlyStats(w http.ResponseWriter, r *http.Request) {
	months := 12
	if m := r.URL.Query().Get("months"); m != "" {
		fmt.Sscanf(m, "%d", &months)
	}
	if months > 36 {
		months = 36
	}

	stats, err := h.store.GetMonthlyStats(months)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleProviderStats returns per-provider breakdown.
func (h *Handler) HandleProviderStats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}

	stats, err := h.store.GetProviderPeriodStats(days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleModelStats returns per-model cost breakdown.
func (h *Handler) HandleModelStats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		fmt.Sscanf(d, "%d", &days)
	}

	stats, err := h.store.GetModelCostStats(days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// maskKey masks an API key, showing only first 6 and last 4 chars.
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 10 {
		return strings.Repeat("•", len(key))
	}
	return key[:6] + strings.Repeat("•", len(key)-10) + key[len(key)-4:]
}
