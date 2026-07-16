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

// CohereProvider handles Cohere's Chat API.
type CohereProvider struct {
	apiKey string
	models []string
	client *http.Client
}

func NewCohereProvider(apiKey string) *CohereProvider {
	return &CohereProvider{
		apiKey: apiKey,
		models: []string{"command-r-plus", "command-r"},
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *CohereProvider) Name() string { return "cohere" }

func (p *CohereProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// Cohere Chat API types

type cohereRequest struct {
	Model         string          `json:"model"`
	Message       string          `json:"message"`
	ChatHistory   []cohereMessage `json:"chat_history,omitempty"`
	Preamble      string          `json:"preamble,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	P             *float64        `json:"p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
}

type cohereMessage struct {
	Role    string `json:"role"`
	Message string `json:"message"`
}

type cohereResponse struct {
	ResponseID   string      `json:"response_id"`
	Text         string      `json:"text"`
	Meta         *cohereMeta `json:"meta"`
	FinishReason string      `json:"finish_reason"`
}

type cohereMeta struct {
	Tokens *cohereTokens `json:"tokens"`
}

type cohereTokens struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (p *CohereProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	cReq := cohereRequest{
		Model:         req.Model,
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		P:             req.TopP,
		StopSequences: req.Stop,
	}

	// Convert messages: last user message → message, rest → chat_history, system → preamble
	for i, msg := range req.Messages {
		text, err := msg.TextContent()
		if err != nil {
			return nil, fmt.Errorf("cohere adapter only supports text content: %w", err)
		}
		switch msg.Role {
		case "system":
			cReq.Preamble = text
		case "user":
			if i == len(req.Messages)-1 {
				cReq.Message = text
			} else {
				cReq.ChatHistory = append(cReq.ChatHistory, cohereMessage{
					Role: "USER", Message: text,
				})
			}
		case "assistant":
			cReq.ChatHistory = append(cReq.ChatHistory, cohereMessage{
				Role: "CHATBOT", Message: text,
			})
		default:
			return nil, fmt.Errorf("cohere does not support %q messages", msg.Role)
		}
	}

	// If last message isn't user, just use the last user message
	if cReq.Message == "" {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				text, err := req.Messages[i].TextContent()
				if err != nil {
					return nil, fmt.Errorf("cohere adapter only supports text content: %w", err)
				}
				cReq.Message = text
				break
			}
		}
	}
	if cReq.Message == "" {
		return nil, fmt.Errorf("cohere requires at least one user message")
	}

	body, err := json.Marshal(cReq)
	if err != nil {
		return nil, fmt.Errorf("marshal cohere request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cohere.ai/v1/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cohere request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cohere returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var cResp cohereResponse
	if err := json.Unmarshal(respBody, &cResp); err != nil {
		return nil, fmt.Errorf("unmarshal cohere response: %w", err)
	}

	usage := Usage{}
	if cResp.Meta != nil && cResp.Meta.Tokens != nil {
		usage = Usage{
			PromptTokens:     cResp.Meta.Tokens.InputTokens,
			CompletionTokens: cResp.Meta.Tokens.OutputTokens,
			TotalTokens:      cResp.Meta.Tokens.InputTokens + cResp.Meta.Tokens.OutputTokens,
		}
	}

	finishReason := "stop"
	if cResp.FinishReason == "MAX_TOKENS" {
		finishReason = "length"
	}

	return &ChatResponse{
		ID:      cResp.ResponseID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: NewTextContent(cResp.Text)},
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}, nil
}
