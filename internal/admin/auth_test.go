package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sabahattink/llm-gateway/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func newAdminTestStore(t *testing.T) *storage.Store {
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

func TestAuthMiddlewareProtectsStatsAndLogs(t *testing.T) {
	store := newAdminTestStore(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("very-secure-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt error = %v", err)
	}
	if err := store.SetAdminPassword(string(hash)); err != nil {
		t.Fatalf("SetAdminPassword() error = %v", err)
	}

	protected := AuthMiddleware(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/api/stats", "/api/stats/daily", "/api/logs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s returned %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("/health returned %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestServeSetupRequiresTokenForRemoteRequests(t *testing.T) {
	store := newAdminTestStore(t)
	handler := NewAuthHandler(store, []byte("login"), []byte("<html>setup</html>"))

	req := httptest.NewRequest(http.MethodGet, "/admin/setup", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	handler.ServeSetup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote setup without token returned %d, want %d", rec.Code, http.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/setup?token="+handler.SetupToken(), nil)
	req.RemoteAddr = "203.0.113.10:1234"
	rec = httptest.NewRecorder()
	handler.ServeSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote setup with token returned %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleSetupAcceptsValidRemoteToken(t *testing.T) {
	store := newAdminTestStore(t)
	handler := NewAuthHandler(store, []byte("login"), []byte("setup"))

	body := []byte(`{"password":"very-secure-password","password_confirm":"very-secure-password","setup_token":"` + handler.SetupToken() + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/setup", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.10:1234"
	rec := httptest.NewRecorder()
	handler.ServeSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ServeSetup() returned %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !store.HasAdminPassword() {
		t.Fatalf("password was not stored after successful setup")
	}
}
