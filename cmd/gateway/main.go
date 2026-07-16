package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/sabahattink/llm-gateway/internal/admin"
	"github.com/sabahattink/llm-gateway/internal/middleware"
	"github.com/sabahattink/llm-gateway/internal/providers"
	"github.com/sabahattink/llm-gateway/internal/proxy"
	"github.com/sabahattink/llm-gateway/internal/storage"
)

var (
	globalRegistry *providers.Registry
	globalStore    *storage.Store
	registryMu     sync.Mutex
	version        = "dev"
)

func main() {
	port := envOr("PORT", "8080")
	dbPath := envOr("DB_PATH", "gateway.db")

	// Handle commands that do not start the server.
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			fmt.Println(versionOutput())
			return
		}
		if arg == "--reset-password" {
			store, err := storage.New(dbPath)
			if err != nil {
				log.Fatalf("failed to open db: %v", err)
			}
			if err := store.ResetAdminPassword(); err != nil {
				log.Fatalf("failed to reset password: %v", err)
			}
			fmt.Println("Admin password has been reset. Restart the gateway and visit /admin/setup to set a new password.")
			store.Close()
			os.Exit(0)
		}
	}

	// Initialize storage
	store, err := storage.New(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize storage: %v", err)
	}
	defer store.Close()
	globalStore = store

	// Initialize provider registry
	globalRegistry = providers.NewRegistry()
	registerProviders(globalRegistry, store)

	// Load HTML files
	dashboardHTML := loadHTML("dashboard.html")
	settingsHTML := loadHTML("settings.html")
	analyticsHTML := loadHTML("analytics.html")
	loginHTML := loadHTML("login.html")
	setupHTML := loadHTML("setup.html")

	// Create handlers
	router := proxy.NewRouter(globalRegistry, store)
	adminHandler := admin.NewHandler(store, dashboardHTML, settingsHTML, analyticsHTML, reloadProviders)
	authHandler := admin.NewAuthHandler(store, loginHTML, setupHTML)

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Public API endpoint with optional Bearer authentication.
	var chatHandler http.Handler = http.HandlerFunc(router.HandleChatCompletion)
	if apiKey := os.Getenv("LLM_GATEWAY_API_KEY"); apiKey != "" {
		if len(apiKey) < 32 {
			log.Fatalf("LLM_GATEWAY_API_KEY must be at least 32 characters")
		}
		chatHandler = middleware.APIKeyAuth(apiKey, chatHandler)
		log.Printf("Public API authentication: enabled")
	} else {
		log.Printf("WARNING: Public API authentication is disabled; set LLM_GATEWAY_API_KEY before exposing the gateway")
	}
	if rateLimit := envInt("PUBLIC_RATE_LIMIT_RPM", 60); rateLimit > 0 {
		chatHandler = middleware.NewRateLimiter(rateLimit, time.Minute).Middleware(chatHandler)
		log.Printf("Public chat rate limit: %d requests/minute per IP", rateLimit)
	} else {
		log.Printf("Public chat rate limit: disabled")
	}
	mux.Handle("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/health", router.HandleHealth)

	// Auth endpoints (no auth needed)
	mux.HandleFunc("/admin/login", authHandler.ServeLogin)
	mux.HandleFunc("/admin/setup", authHandler.ServeSetup)
	mux.HandleFunc("/admin/logout", authHandler.HandleLogout)

	// Dashboard data APIs
	mux.HandleFunc("/api/stats", router.HandleStats)
	mux.HandleFunc("/api/logs", router.HandleLogs)

	// Settings API (protected by auth middleware)
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			adminHandler.HandleGetSettings(w, r)
		case http.MethodPost:
			adminHandler.HandleSaveSetting(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/settings/test", adminHandler.HandleTestProvider)
	mux.HandleFunc("/api/settings/delete", adminHandler.HandleDeleteSetting)

	// Analytics API endpoints
	mux.HandleFunc("/api/stats/daily", adminHandler.HandleDailyStats)
	mux.HandleFunc("/api/stats/monthly", adminHandler.HandleMonthlyStats)
	mux.HandleFunc("/api/stats/providers", adminHandler.HandleProviderStats)
	mux.HandleFunc("/api/stats/models", adminHandler.HandleModelStats)

	// Admin pages (protected)
	mux.HandleFunc("/admin", adminHandler.ServeDashboard)
	mux.HandleFunc("/admin/settings", adminHandler.ServeSettings)
	mux.HandleFunc("/admin/analytics", adminHandler.ServeAnalytics)
	mux.HandleFunc("/api/dashboard", adminHandler.HandleDashboardData)

	// Apply middleware: logging → auth
	handler := middleware.Logging(admin.AuthMiddleware(store, mux))

	log.Printf("LLM Gateway starting on :%s", port)
	log.Printf("Registered providers: %v", globalRegistry.ListProviders())
	if store.HasAdminPassword() {
		log.Printf("Admin auth: enabled (login at /admin/login)")
	} else {
		log.Printf("Admin auth: not configured (setup at /admin/setup)")
		if token := authHandler.SetupToken(); token != "" {
			log.Printf("Remote setup token: %s", token)
			log.Printf("Remote setup URL: /admin/setup?token=%s", token)
		}
	}
	log.Printf("POST /v1/chat/completions — proxy endpoint")
	log.Printf("GET  /health              — health check")
	log.Printf("GET  /admin               — dashboard")
	log.Printf("GET  /admin/settings      — provider settings")
	log.Printf("GET  /admin/analytics     — spending analytics")

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	go func() {
		<-shutdownSignals
		log.Printf("Shutdown signal received")

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown failed: %v", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
	log.Printf("LLM Gateway stopped")
}

func versionOutput() string {
	return "llm-gateway " + version
}

func reloadProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()

	newRegistry := providers.NewRegistry()
	registerProviders(newRegistry, globalStore)

	globalRegistry.ReplaceWith(newRegistry)
	log.Printf("Providers reloaded: %v", globalRegistry.ListProviders())
}

func loadHTML(filename string) []byte {
	path := findFile(filename)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("warning: %s not found at %s", filename, path)
		return []byte(fmt.Sprintf("<html><body><h1>%s not found</h1></body></html>", filename))
	}
	return data
}

func findFile(filename string) string {
	candidates := []string{
		"web/" + filename,
		"../../web/" + filename,
	}
	if ex, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(ex), "web", filename))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "web/" + filename
}

func registerProviders(registry *providers.Registry, store *storage.Store) {
	getKey := func(envName, provider string) string {
		if v := os.Getenv(envName); v != "" {
			return v
		}
		if store != nil {
			return store.GetProviderAPIKey(provider)
		}
		return ""
	}

	getURL := func(envName, provider, defaultURL string) string {
		if v := os.Getenv(envName); v != "" {
			return v
		}
		if store != nil {
			if s, _ := store.GetProviderSetting(provider); s != nil && s.BaseURL != "" {
				return s.BaseURL
			}
		}
		return defaultURL
	}

	if key := getKey("ANTHROPIC_API_KEY", "anthropic"); key != "" {
		registry.Register(providers.NewAnthropicProvider(key))
		log.Println("  + anthropic (claude-opus-4, claude-sonnet-4, claude-haiku-4)")
	}

	if key := getKey("OPENAI_API_KEY", "openai"); key != "" {
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "openai",
			BaseURL: "https://api.openai.com",
			APIKey:  key,
			Models:  []string{"gpt-4o", "gpt-4o-mini", "o1", "o3-mini"},
		}))
		log.Println("  + openai (gpt-4o, gpt-4o-mini, o1, o3-mini)")
	}

	if key := getKey("GOOGLE_API_KEY", "google"); key != "" {
		registry.Register(providers.NewGeminiProvider(key))
		log.Println("  + google (gemini-2.0-flash, gemini-1.5-pro)")
	}

	if key := getKey("GROQ_API_KEY", "groq"); key != "" {
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "groq",
			BaseURL: "https://api.groq.com/openai",
			APIKey:  key,
			Models:  []string{"llama-3.3-70b-versatile", "mixtral-8x7b-32768"},
		}))
		log.Println("  + groq (llama-3.3-70b, mixtral-8x7b)")
	}

	if key := getKey("MISTRAL_API_KEY", "mistral"); key != "" {
		registry.Register(providers.NewMistralProvider(key))
		log.Println("  + mistral (mistral-large, mistral-small, codestral)")
	}

	if key := getKey("COHERE_API_KEY", "cohere"); key != "" {
		registry.Register(providers.NewCohereProvider(key))
		log.Println("  + cohere (command-r-plus, command-r)")
	}

	if key := getKey("XAI_API_KEY", "xai"); key != "" {
		registry.Register(providers.NewXAIProvider(key))
		log.Println("  + xai (grok-2, grok-2-mini)")
	}

	if key := getKey("PERPLEXITY_API_KEY", "perplexity"); key != "" {
		registry.Register(providers.NewPerplexityProvider(key))
		log.Println("  + perplexity (sonar-large, sonar-small)")
	}

	if key := getKey("TOGETHER_API_KEY", "together"); key != "" {
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "together",
			BaseURL: "https://api.together.xyz",
			APIKey:  key,
			Models:  []string{"meta-llama/Llama-3-70b-chat-hf"},
		}))
		log.Println("  + together (meta-llama/Llama-3-70b)")
	}

	isLocalEnabled := func(envEnabled, provider string) bool {
		if os.Getenv(envEnabled) == "true" {
			return true
		}
		if store != nil {
			if s, _ := store.GetProviderSetting(provider); s != nil && s.IsEnabled {
				return true
			}
		}
		return false
	}

	if isLocalEnabled("OLLAMA_ENABLED", "ollama") {
		ollamaURL := getURL("OLLAMA_BASE_URL", "ollama", "http://localhost:11434")
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "ollama",
			BaseURL: ollamaURL,
			Models:  []string{"llama3", "mistral", "codellama", "phi3"},
		}))
		log.Println("  + ollama (local)")
	}

	if isLocalEnabled("LMSTUDIO_ENABLED", "lmstudio") {
		lmStudioURL := getURL("LMSTUDIO_BASE_URL", "lmstudio", "http://localhost:1234")
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "lmstudio",
			BaseURL: lmStudioURL,
			Models:  []string{},
		}))
		log.Println("  + lmstudio (local)")
	}

	if isLocalEnabled("VLLM_ENABLED", "vllm") {
		vllmURL := getURL("VLLM_BASE_URL", "vllm", "http://localhost:8000")
		registry.Register(providers.NewOpenAIProvider(providers.OpenAIConfig{
			Name:    "vllm",
			BaseURL: vllmURL,
			Models:  []string{},
		}))
		log.Println("  + vllm (local)")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}
