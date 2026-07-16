package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sabahattink/llm-gateway/internal/middleware"
	"github.com/sabahattink/llm-gateway/internal/providers"
	"github.com/sabahattink/llm-gateway/internal/storage"
)

const maxChatRequestBytes = 2 << 20

type Router struct {
	registry *providers.Registry
	store    *storage.Store
}

func NewRouter(registry *providers.Registry, store *storage.Store) *Router {
	return &Router{
		registry: registry,
		store:    store,
	}
}

func (rt *Router) HandleChatCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxChatRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large", "request_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to read request body", "invalid_request")
		return
	}
	defer r.Body.Close()

	var req providers.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "invalid_request")
		return
	}

	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model field is required", "invalid_request")
		return
	}

	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages field is required", "invalid_request")
		return
	}

	// Resolve provider
	provider, err := rt.registry.Resolve(req.Model)
	if err != nil {
		rt.logRequest(storage.RequestLog{
			Model:        req.Model,
			Provider:     "unknown",
			StatusCode:   http.StatusBadRequest,
			ErrorMessage: err.Error(),
			ClientIP:     middleware.ClientIP(r),
		})
		writeError(w, http.StatusBadRequest, err.Error(), "model_not_found")
		return
	}

	// ─── Streaming path ───
	if req.Stream {
		rt.handleStream(w, r, provider, req)
		return
	}

	// ─── Non-streaming path ───
	start := time.Now()
	resp, err := provider.ChatCompletion(r.Context(), req)
	latency := time.Since(start)

	// Prepare log entry
	logEntry := storage.RequestLog{
		Model:     req.Model,
		Provider:  provider.Name(),
		LatencyMs: latency.Milliseconds(),
		ClientIP:  middleware.ClientIP(r),
	}

	if err != nil {
		logEntry.StatusCode = http.StatusBadGateway
		logEntry.ErrorMessage = err.Error()
		rt.logRequest(logEntry)
		writeError(w, http.StatusBadGateway, "provider error: "+err.Error(), "provider_error")
		return
	}

	// Success
	logEntry.StatusCode = http.StatusOK
	logEntry.PromptTokens = resp.Usage.PromptTokens
	logEntry.CompletionTokens = resp.Usage.CompletionTokens
	logEntry.TotalTokens = resp.Usage.TotalTokens
	rt.logRequest(logEntry)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-LLM-Provider", provider.Name())
	w.Header().Set("X-LLM-Latency-Ms", fmt.Sprintf("%d", latency.Milliseconds()))
	json.NewEncoder(w).Encode(resp)
}

// handleStream handles SSE streaming requests.
func (rt *Router) handleStream(w http.ResponseWriter, r *http.Request, provider providers.Provider, req providers.ChatRequest) {
	sp, ok := provider.(providers.StreamProvider)
	if !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("provider %q does not support streaming", provider.Name()),
			"streaming_not_supported")
		return
	}

	// Set SSE headers before writing any data
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx/proxy buffering
	w.Header().Set("X-LLM-Provider", provider.Name())

	start := time.Now()
	usage, err := sp.ChatCompletionStream(r.Context(), req, w)
	latency := time.Since(start)

	// Log the streaming request
	logEntry := storage.RequestLog{
		Model:     req.Model,
		Provider:  provider.Name(),
		LatencyMs: latency.Milliseconds(),
		ClientIP:  middleware.ClientIP(r),
	}

	if err != nil {
		logEntry.StatusCode = http.StatusBadGateway
		logEntry.ErrorMessage = err.Error()
		rt.logRequest(logEntry)
		// Can't write error response after streaming has started — log only
		log.Printf("streaming error for %s/%s: %v", provider.Name(), req.Model, err)
		return
	}

	logEntry.StatusCode = http.StatusOK
	if usage != nil {
		logEntry.PromptTokens = usage.PromptTokens
		logEntry.CompletionTokens = usage.CompletionTokens
		logEntry.TotalTokens = usage.TotalTokens
	}
	rt.logRequest(logEntry)
}

func (rt *Router) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"providers": rt.registry.ListProviders(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (rt *Router) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := rt.store.GetStats(24 * time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get stats", "internal_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (rt *Router) HandleLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := rt.store.GetRecentLogs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get logs", "internal_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (rt *Router) logRequest(entry storage.RequestLog) {
	if err := rt.store.LogRequest(entry); err != nil {
		log.Printf("failed to log request: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(providers.ErrorResponse{
		Error: struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}{
			Message: message,
			Type:    "error",
			Code:    code,
		},
	})
}
