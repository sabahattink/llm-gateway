package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sabahattink/llm-gateway/internal/providers"
	"github.com/sabahattink/llm-gateway/internal/storage"
)

type fakeProvider struct {
	response    *providers.ChatResponse
	err         error
	streamUsage *providers.Usage
}

func (p *fakeProvider) Name() string {
	return "fake"
}

func (p *fakeProvider) SupportsModel(model string) bool {
	return model == "test-model"
}

func (p *fakeProvider) ChatCompletion(context.Context, providers.ChatRequest) (*providers.ChatResponse, error) {
	return p.response, p.err
}

func (p *fakeProvider) ChatCompletionStream(_ context.Context, _ providers.ChatRequest, w http.ResponseWriter) (*providers.Usage, error) {
	if p.err != nil {
		return nil, p.err
	}
	providers.WriteSSEChunk(w, providers.StreamChunk{
		ID:      "chunk-1",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "test-model",
		Choices: []providers.StreamChoice{{
			Index: 0,
			Delta: providers.StreamDelta{Content: "hello"},
		}},
	})
	providers.WriteSSEDone(w)
	return p.streamUsage, nil
}

func newProxyTestStore(t *testing.T) *storage.Store {
	t.Helper()

	store, err := storage.New(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("storage.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestHandleChatCompletionRejectsOversizedBody(t *testing.T) {
	router := NewRouter(providers.NewRegistry(), newProxyTestStore(t))

	body := bytes.Repeat([]byte("a"), maxChatRequestBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	router.HandleChatCompletion(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("HandleChatCompletion() returned %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleChatCompletionRoutesAndLogsSuccess(t *testing.T) {
	store := newProxyTestStore(t)
	registry := providers.NewRegistry()
	registry.Register(&fakeProvider{response: &providers.ChatResponse{
		ID:      "chat-1",
		Object:  "chat.completion",
		Created: 1,
		Model:   "test-model",
		Choices: []providers.Choice{{
			Index:        0,
			Message:      providers.Message{Role: "assistant", Content: providers.NewTextContent("hello")},
			FinishReason: "stop",
		}},
		Usage: providers.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
	}})
	router := NewRouter(registry, store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()

	router.HandleChatCompletion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-LLM-Provider"); got != "fake" {
		t.Fatalf("X-LLM-Provider = %q", got)
	}
	var response providers.ChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "chat-1" || response.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected response: %#v", response)
	}

	logs, err := store.GetRecentLogs(1)
	if err != nil {
		t.Fatalf("GetRecentLogs() error = %v", err)
	}
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].Provider != "fake" ||
		logs[0].TotalTokens != 3 || logs[0].ClientIP != "203.0.113.10" {
		t.Fatalf("unexpected request log: %#v", logs)
	}
}

func TestHandleChatCompletionLogsProviderError(t *testing.T) {
	store := newProxyTestStore(t)
	registry := providers.NewRegistry()
	registry.Register(&fakeProvider{err: errors.New("upstream unavailable")})
	router := NewRouter(registry, store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`,
	))
	rec := httptest.NewRecorder()

	router.HandleChatCompletion(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response providers.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != "provider_error" {
		t.Fatalf("error code = %q", response.Error.Code)
	}

	logs, err := store.GetRecentLogs(1)
	if err != nil {
		t.Fatalf("GetRecentLogs() error = %v", err)
	}
	if len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway ||
		!strings.Contains(logs[0].ErrorMessage, "upstream unavailable") {
		t.Fatalf("unexpected request log: %#v", logs)
	}
}

func TestHandleChatCompletionStreamsAndLogsUsage(t *testing.T) {
	store := newProxyTestStore(t)
	registry := providers.NewRegistry()
	registry.Register(&fakeProvider{
		streamUsage: &providers.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6},
	})
	router := NewRouter(registry, store)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
	))
	rec := httptest.NewRecorder()

	router.HandleChatCompletion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected stream body: %q", body)
	}

	logs, err := store.GetRecentLogs(1)
	if err != nil {
		t.Fatalf("GetRecentLogs() error = %v", err)
	}
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].TotalTokens != 6 {
		t.Fatalf("unexpected request log: %#v", logs)
	}
}
