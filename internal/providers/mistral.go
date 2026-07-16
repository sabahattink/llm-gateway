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

// MistralProvider handles Mistral AI's chat API.
// Mistral's API is very close to OpenAI's format but has its own endpoint.
type MistralProvider struct {
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

func NewMistralProvider(apiKey string) *MistralProvider {
	return &MistralProvider{
		apiKey: apiKey,
		models: []string{
			"mistral-large",
			"mistral-large-latest",
			"mistral-small",
			"mistral-small-latest",
			"codestral",
			"codestral-latest",
		},
		client:       &http.Client{Timeout: 120 * time.Second},
		streamClient: &http.Client{},
	}
}

func (p *MistralProvider) Name() string { return "mistral" }

func (p *MistralProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

func (p *MistralProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Mistral uses OpenAI-compatible format
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.mistral.ai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mistral request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mistral returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// ChatCompletionStream implements StreamProvider — Mistral uses OpenAI-compatible SSE.
func (p *MistralProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	return openAIPassthroughStreamWithClient(
		ctx, p.streamClient, "https://api.mistral.ai/v1/chat/completions", p.apiKey, req, w,
	)
}
