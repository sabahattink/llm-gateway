package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sabahattink/llm-gateway/internal/storage"
)

func TestHandleSettingsLifecycleMasksSecrets(t *testing.T) {
	store := newAdminTestStore(t)
	reloads := 0
	handler := NewHandler(store, nil, nil, nil, func() {
		reloads++
	})

	saveReq := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(
		`{"provider":"openai","api_key":"sk-test-secret-value"}`,
	))
	saveRec := httptest.NewRecorder()
	handler.HandleSaveSetting(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", saveRec.Code, saveRec.Body.String())
	}
	if reloads != 1 {
		t.Fatalf("reload count after save = %d", reloads)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getRec := httptest.NewRecorder()
	handler.HandleGetSettings(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", getRec.Code, getRec.Body.String())
	}

	var settings []struct {
		Provider  string `json:"provider"`
		MaskedKey string `json:"masked_key"`
		HasKey    bool   `json:"has_key"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	var openAI *struct {
		Provider  string `json:"provider"`
		MaskedKey string `json:"masked_key"`
		HasKey    bool   `json:"has_key"`
	}
	for i := range settings {
		if settings[i].Provider == "openai" {
			openAI = &settings[i]
			break
		}
	}
	if openAI == nil || !openAI.HasKey || openAI.MaskedKey == "sk-test-secret-value" ||
		!strings.HasPrefix(openAI.MaskedKey, "sk-tes") || !strings.HasSuffix(openAI.MaskedKey, "alue") {
		t.Fatalf("unexpected OpenAI setting: %#v", openAI)
	}
	if strings.Contains(getRec.Body.String(), "sk-test-secret-value") {
		t.Fatal("settings response exposed API key")
	}

	deleteReq := httptest.NewRequest(http.MethodPost, "/api/settings/delete", strings.NewReader(
		`{"provider":"openai"}`,
	))
	deleteRec := httptest.NewRecorder()
	handler.HandleDeleteSetting(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if reloads != 2 {
		t.Fatalf("reload count after delete = %d", reloads)
	}
	deleted, err := store.GetProviderSetting("openai")
	if err != nil || deleted != nil {
		t.Fatalf("deleted setting = %#v, %v", deleted, err)
	}
}

func TestHandleSaveSettingRejectsUnknownProvider(t *testing.T) {
	handler := NewHandler(newAdminTestStore(t), nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(
		`{"provider":"unknown","api_key":"secret"}`,
	))
	rec := httptest.NewRecorder()

	handler.HandleSaveSetting(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAnalyticsHandlersReturnStoredDataWithBoundedRanges(t *testing.T) {
	store := newAdminTestStore(t)
	if err := store.LogRequest(storage.RequestLog{
		Model:            "gpt-test",
		Provider:         "openai",
		PromptTokens:     3,
		CompletionTokens: 2,
		TotalTokens:      5,
		StatusCode:       http.StatusOK,
	}); err != nil {
		t.Fatalf("LogRequest() error = %v", err)
	}
	handler := NewHandler(store, nil, nil, nil, nil)

	tests := []struct {
		name    string
		url     string
		handler http.HandlerFunc
	}{
		{name: "daily minimum", url: "/api/stats/daily?days=-10", handler: handler.HandleDailyStats},
		{name: "monthly maximum", url: "/api/stats/monthly?months=999", handler: handler.HandleMonthlyStats},
		{name: "provider fallback", url: "/api/stats/providers?days=invalid", handler: handler.HandleProviderStats},
		{name: "model maximum", url: "/api/stats/models?days=999999", handler: handler.HandleModelStats},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			test.handler(rec, httptest.NewRequest(http.MethodGet, test.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if rec.Body.String() == "[]\n" {
				t.Fatalf("handler returned no analytics data")
			}
		})
	}
}

func TestBoundedQueryInt(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "", want: 30},
		{value: "invalid", want: 30},
		{value: "-5", want: 1},
		{value: "10", want: 10},
		{value: "999", want: 365},
	}
	for _, test := range tests {
		req := httptest.NewRequest(http.MethodGet, "/?days="+test.value, nil)
		if got := boundedQueryInt(req, "days", 30, 1, 365); got != test.want {
			t.Fatalf("boundedQueryInt(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}
