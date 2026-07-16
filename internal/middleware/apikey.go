package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// APIKeyAuth requires an OpenAI-compatible Bearer token.
func APIKeyAuth(apiKey string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(apiKey))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" ||
			strings.ContainsAny(token, " \t\r\n") {
			writeAPIKeyError(w)
			return
		}

		actual := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			writeAPIKeyError(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeAPIKeyError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="llm-gateway"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": "invalid or missing API key",
			"type":    "authentication_error",
			"code":    "invalid_api_key",
		},
	})
}
