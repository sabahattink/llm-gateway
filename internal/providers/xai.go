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

// XAIProvider handles xAI's Grok API.
// xAI uses an OpenAI-compatible format with its own endpoint.
type XAIProvider struct {
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

func NewXAIProvider(apiKey string) *XAIProvider {
	return &XAIProvider{
		apiKey:       apiKey,
		models:       []string{"grok-2", "grok-2-mini"},
		client:       &http.Client{Timeout: 120 * time.Second},
		streamClient: &http.Client{},
	}
}

func (p *XAIProvider) Name() string { return "xai" }

func (p *XAIProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

func (p *XAIProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.x.ai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("xai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xai returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// ChatCompletionStream implements StreamProvider — xAI uses OpenAI-compatible SSE.
func (p *XAIProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	return openAIPassthroughStreamWithClient(
		ctx, p.streamClient, "https://api.x.ai/v1/chat/completions", p.apiKey, req, w,
	)
}
