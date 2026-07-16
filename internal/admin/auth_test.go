package admin

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestHandleSetupValidatesPassword(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "short password",
			body: `{"password":"short","password_confirm":"short"}`,
		},
		{
			name: "password mismatch",
			body: `{"password":"very-secure-password","password_confirm":"different-password"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newAdminTestStore(t)
			handler := NewAuthHandler(store, []byte("login"), []byte("setup"))
			req := httptest.NewRequest(http.MethodPost, "/admin/setup", strings.NewReader(test.body))
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()

			handler.ServeSetup(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if store.HasAdminPassword() {
				t.Fatal("invalid setup stored an admin password")
			}
		})
	}
}

func TestLoginCreatesSecureSessionAndLogoutDeletesIt(t *testing.T) {
	store := newAdminTestStore(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("very-secure-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt error = %v", err)
	}
	if err := store.SetAdminPassword(string(hash)); err != nil {
		t.Fatalf("SetAdminPassword() error = %v", err)
	}
	handler := NewAuthHandler(store, []byte("login"), []byte("setup"))

	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(
		`{"password":"very-secure-password"}`,
	))
	req.RemoteAddr = "192.0.2.10:1234"
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()

	handler.ServeLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("login response = %#v", body)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d", len(cookies))
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != cookieName || !sessionCookie.HttpOnly || !sessionCookie.Secure ||
		sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %#v", sessionCookie)
	}
	if !store.ValidateSession(sessionCookie.Value) {
		t.Fatal("login session is not valid")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	logoutReq.TLS = &tls.ConnectionState{}
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	handler.HandleLogout(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusFound {
		t.Fatalf("logout status = %d", logoutRec.Code)
	}
	if store.ValidateSession(sessionCookie.Value) {
		t.Fatal("logout did not delete session")
	}
	expired := logoutRec.Result().Cookies()
	if len(expired) != 1 || expired[0].MaxAge != -1 || !expired[0].Secure {
		t.Fatalf("unexpected logout cookie: %#v", expired)
	}
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	store := newAdminTestStore(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("very-secure-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt error = %v", err)
	}
	if err := store.SetAdminPassword(string(hash)); err != nil {
		t.Fatalf("SetAdminPassword() error = %v", err)
	}
	handler := NewAuthHandler(store, []byte("login"), []byte("setup"))

	login := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(
			`{"password":"wrong-password"}`,
		))
		req.RemoteAddr = "192.0.2.20:1234"
		rec := httptest.NewRecorder()
		handler.ServeLogin(rec, req)
		return rec
	}

	for i := 0; i < 5; i++ {
		if code := login().Code; code != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d", i+1, code)
		}
	}
	if code := login().Code; code != http.StatusTooManyRequests {
		t.Fatalf("locked status = %d", code)
	}
}
