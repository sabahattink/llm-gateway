package middleware

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

func TestLoggingPreservesStreamingFlush(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := Logging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped ResponseWriter does not implement http.Flusher")
		}
		_, _ = w.Write([]byte("data: first\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: second\n\n"))
	}))

	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
	})

	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.flushes != 1 {
		t.Fatalf("flush count = %d, want 1", rec.flushes)
	}
	if rec.Body.String() != "data: first\n\ndata: second\n\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestStatusWriterKeepsFirstStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusInternalServerError)

	if rec.Code != http.StatusCreated || writer.status != http.StatusCreated {
		t.Fatalf("status = recorder:%d writer:%d", rec.Code, writer.status)
	}
}

func TestRateLimiterLimitsResetsAndCleansStaleClients(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(2, 90*time.Second)
	limiter.now = func() time.Time { return now }
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if code := request("192.0.2.1").Code; code != http.StatusNoContent {
		t.Fatalf("first status = %d", code)
	}
	if code := request("192.0.2.1").Code; code != http.StatusNoContent {
		t.Fatalf("second status = %d", code)
	}
	limited := request("192.0.2.1")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d", limited.Code)
	}
	if got := limited.Header().Get("Retry-After"); got != "90" {
		t.Fatalf("Retry-After = %q, want 90", got)
	}

	_ = request("192.0.2.2")
	if len(limiter.clients) != 2 {
		t.Fatalf("client count = %d, want 2", len(limiter.clients))
	}

	now = now.Add(90 * time.Second)
	if code := request("192.0.2.1").Code; code != http.StatusNoContent {
		t.Fatalf("status after reset = %d", code)
	}
	if len(limiter.clients) != 1 {
		t.Fatalf("stale clients were not cleaned: %d", len(limiter.clients))
	}
}

func TestRequestIdentityIgnoresProxyHeadersByDefault(t *testing.T) {
	previous := trustProxyHeaders
	trustProxyHeaders = false
	t.Cleanup(func() {
		trustProxyHeaders = previous
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := ClientIP(req); got != "192.0.2.10" {
		t.Fatalf("ClientIP() = %q", got)
	}
	if RequestIsHTTPS(req) {
		t.Fatal("RequestIsHTTPS() trusted an untrusted proxy header")
	}
}

func TestRequestIdentityUsesTrustedProxyHeaders(t *testing.T) {
	previous := trustProxyHeaders
	trustProxyHeaders = true
	t.Cleanup(func() {
		trustProxyHeaders = previous
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 192.0.2.10")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := ClientIP(req); got != "198.51.100.1" {
		t.Fatalf("ClientIP() = %q", got)
	}
	if !RequestIsHTTPS(req) {
		t.Fatal("RequestIsHTTPS() = false")
	}
}

func TestRequestIsHTTPSAndLoopbackWithoutProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:1234"
	req.TLS = &tls.ConnectionState{}

	if !IsLoopbackRequest(req) {
		t.Fatal("IsLoopbackRequest() = false")
	}
	if !RequestIsHTTPS(req) {
		t.Fatal("RequestIsHTTPS() = false for TLS request")
	}
}

func TestAPIKeyAuth(t *testing.T) {
	handler := APIKeyAuth(
		"0123456789abcdef0123456789abcdef",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic abc", wantStatus: http.StatusUnauthorized},
		{name: "wrong key", authorization: "Bearer wrong", wantStatus: http.StatusUnauthorized},
		{name: "extra token data", authorization: "Bearer 0123456789abcdef0123456789abcdef extra", wantStatus: http.StatusUnauthorized},
		{name: "valid", authorization: "Bearer 0123456789abcdef0123456789abcdef", wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", authorization: "bearer 0123456789abcdef0123456789abcdef", wantStatus: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if test.authorization != "" {
				req.Header.Set("Authorization", test.authorization)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, test.wantStatus)
			}
			if test.wantStatus == http.StatusUnauthorized &&
				rec.Header().Get("WWW-Authenticate") != `Bearer realm="llm-gateway"` {
				t.Fatalf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}
