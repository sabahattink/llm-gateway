package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sabahattink/llm-gateway/internal/providers"
	"github.com/sabahattink/llm-gateway/internal/storage"
)

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
