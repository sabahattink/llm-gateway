package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const anthropicAPIVersion = "2023-06-01"

// AnthropicProvider handles the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		models: []string{
			"claude-sonnet-4-6",
			"claude-opus-4-6",
			"claude-opus-4-5-20251101",
			"claude-haiku-4-5-20251001",
			"claude-sonnet-4-5-20250929",
			"claude-opus-4-1-20250805",
			"claude-opus-4-20250514",
			"claude-sonnet-4-20250514",
			"claude-3-haiku-20240307",
		},
		client:       &http.Client{Timeout: 120 * time.Second},
		streamClient: &http.Client{},
	}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// Anthropic Messages API types

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stop        []string           `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (p *AnthropicProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert OpenAI format → Anthropic Messages format
	aReq := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   4096,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
	}

	if req.MaxTokens != nil {
		aReq.MaxTokens = *req.MaxTokens
	}

	// Extract system message and convert the rest
	for _, msg := range req.Messages {
		text, err := msg.TextContent()
		if err != nil {
			return nil, fmt.Errorf("anthropic only supports text content: %w", err)
		}
		if msg.Role == "system" {
			aReq.System = text
			continue
		}
		if msg.Role != "user" && msg.Role != "assistant" {
			return nil, fmt.Errorf("anthropic does not support %q messages", msg.Role)
		}
		aReq.Messages = append(aReq.Messages, anthropicMessage{
			Role:    msg.Role,
			Content: text,
		})
	}

	// Anthropic requires at least one message
	if len(aReq.Messages) == 0 {
		return nil, fmt.Errorf("anthropic requires at least one non-system message")
	}

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var aResp anthropicResponse
	if err := json.Unmarshal(respBody, &aResp); err != nil {
		return nil, fmt.Errorf("unmarshal anthropic response: %w", err)
	}

	// Convert Anthropic response → OpenAI format
	content := ""
	for _, c := range aResp.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	finishReason := "stop"
	if aResp.StopReason == "max_tokens" {
		finishReason = "length"
	}

	return &ChatResponse{
		ID:      aResp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   aResp.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: NewTextContent(content)},
				FinishReason: finishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     aResp.Usage.InputTokens,
			CompletionTokens: aResp.Usage.OutputTokens,
			TotalTokens:      aResp.Usage.InputTokens + aResp.Usage.OutputTokens,
		},
	}, nil
}

// ChatCompletionStream implements StreamProvider — converts Anthropic SSE to OpenAI SSE format.
func (p *AnthropicProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	// Build Anthropic request with stream=true
	aReq := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   4096,
		Stream:      true,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
	}
	if req.MaxTokens != nil {
		aReq.MaxTokens = *req.MaxTokens
	}

	for _, msg := range req.Messages {
		text, err := msg.TextContent()
		if err != nil {
			return nil, fmt.Errorf("anthropic only supports text content: %w", err)
		}
		if msg.Role == "system" {
			aReq.System = text
			continue
		}
		if msg.Role != "user" && msg.Role != "assistant" {
			return nil, fmt.Errorf("anthropic does not support %q messages", msg.Role)
		}
		aReq.Messages = append(aReq.Messages, anthropicMessage{
			Role:    msg.Role,
			Content: text,
		})
	}

	if len(aReq.Messages) == 0 {
		return nil, fmt.Errorf("anthropic requires at least one non-system message")
	}

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse Anthropic SSE events and convert to OpenAI SSE format
	var (
		msgID   string
		model   string
		created = time.Now().Unix()
		usage   = &Usage{}
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return usage, fmt.Errorf("decode anthropic stream event: %w", err)
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_start":
			// Extract message metadata
			if msg, ok := event["message"].(map[string]any); ok {
				msgID, _ = msg["id"].(string)
				model, _ = msg["model"].(string)
				if u, ok := msg["usage"].(map[string]any); ok {
					if v, ok := u["input_tokens"].(float64); ok {
						usage.PromptTokens = int(v)
					}
				}
			}
			// Emit role chunk
			WriteSSEChunk(w, StreamChunk{
				ID: msgID, Object: "chat.completion.chunk",
				Created: created, Model: model,
				Choices: []StreamChoice{{
					Index: 0,
					Delta: StreamDelta{Role: "assistant"},
				}},
			})

		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok {
					WriteSSEChunk(w, StreamChunk{
						ID: msgID, Object: "chat.completion.chunk",
						Created: created, Model: model,
						Choices: []StreamChoice{{
							Index: 0,
							Delta: StreamDelta{Content: text},
						}},
					})
				}
			}

		case "message_delta":
			// Extract finish reason and output token count
			finishReason := "stop"
			if delta, ok := event["delta"].(map[string]any); ok {
				if sr, ok := delta["stop_reason"].(string); ok && sr == "max_tokens" {
					finishReason = "length"
				}
			}
			if u, ok := event["usage"].(map[string]any); ok {
				if v, ok := u["output_tokens"].(float64); ok {
					usage.CompletionTokens = int(v)
					usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
				}
			}
			WriteSSEChunk(w, StreamChunk{
				ID: msgID, Object: "chat.completion.chunk",
				Created: created, Model: model,
				Choices: []StreamChoice{{
					Index:        0,
					Delta:        StreamDelta{},
					FinishReason: &finishReason,
				}},
			})

		case "message_stop":
			WriteSSEDone(w)
		}
	}

	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("reading anthropic stream: %w", err)
	}

	return usage, nil
}
