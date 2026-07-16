package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PerplexityProvider handles Perplexity's Sonar API.
// Perplexity uses an OpenAI-compatible format.
type PerplexityProvider struct {
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

func NewPerplexityProvider(apiKey string) *PerplexityProvider {
	return &PerplexityProvider{
		apiKey:       apiKey,
		models:       []string{"sonar-large", "sonar-small"},
		client:       &http.Client{Timeout: 120 * time.Second},
		streamClient: &http.Client{},
	}
}

func (p *PerplexityProvider) Name() string { return "perplexity" }

func (p *PerplexityProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

func (p *PerplexityProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.perplexity.ai/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("perplexity request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("perplexity returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// ChatCompletionStream implements StreamProvider — Perplexity uses OpenAI-compatible SSE.
func (p *PerplexityProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	return openAIPassthroughStreamWithClient(
		ctx, p.streamClient, "https://api.perplexity.ai/chat/completions", p.apiKey, req, w,
	)
}
