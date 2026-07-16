package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider handles OpenAI and all OpenAI-compatible APIs:
// Groq, Ollama, LM Studio, vLLM, Together AI.
type OpenAIProvider struct {
	name         string
	baseURL      string
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

type OpenAIConfig struct {
	Name    string
	BaseURL string
	APIKey  string
	Models  []string
}

func NewOpenAIProvider(cfg OpenAIConfig) *OpenAIProvider {
	return &OpenAIProvider{
		name:    cfg.Name,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		models:  cfg.Models,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		streamClient: &http.Client{},
	}
}

func (p *OpenAIProvider) Name() string {
	return p.name
}

func (p *OpenAIProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider %s request failed: %w", p.name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider %s returned status %d: %s", p.name, resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// ChatCompletionStream implements StreamProvider for OpenAI-compatible APIs.
func (p *OpenAIProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	url := p.baseURL + "/v1/chat/completions"
	return openAIPassthroughStreamWithClient(ctx, p.streamClient, url, p.apiKey, req, w)
}
