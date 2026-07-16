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

// GeminiProvider handles Google Gemini API.
type GeminiProvider struct {
	apiKey       string
	models       []string
	client       *http.Client
	streamClient *http.Client
}

func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey:       apiKey,
		models:       []string{"gemini-2.0-flash", "gemini-1.5-pro"},
		client:       &http.Client{Timeout: 120 * time.Second},
		streamClient: &http.Client{},
	}
}

func (p *GeminiProvider) Name() string { return "google" }

func (p *GeminiProvider) SupportsModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// Gemini API types

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (p *GeminiProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	gReq := geminiRequest{}

	// Build generation config
	gc := &geminiGenerationConfig{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.Stop,
	}
	gReq.GenerationConfig = gc

	// Convert messages
	for _, msg := range req.Messages {
		text, err := msg.TextContent()
		if err != nil {
			return nil, fmt.Errorf("gemini adapter only supports text content: %w", err)
		}
		if msg.Role == "system" {
			gReq.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: text}},
			}
			continue
		}

		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role != "user" && role != "model" {
			return nil, fmt.Errorf("gemini does not support %q messages", msg.Role)
		}

		gReq.Contents = append(gReq.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: text}},
		})
	}
	if len(gReq.Contents) == 0 {
		return nil, fmt.Errorf("gemini requires at least one non-system message")
	}

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		req.Model, p.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var gResp geminiResponse
	if err := json.Unmarshal(respBody, &gResp); err != nil {
		return nil, fmt.Errorf("unmarshal gemini response: %w", err)
	}

	content := ""
	finishReason := "stop"
	if len(gResp.Candidates) > 0 {
		for _, part := range gResp.Candidates[0].Content.Parts {
			content += part.Text
		}
		if gResp.Candidates[0].FinishReason == "MAX_TOKENS" {
			finishReason = "length"
		}
	}

	usage := Usage{}
	if gResp.UsageMetadata != nil {
		usage = Usage{
			PromptTokens:     gResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gResp.UsageMetadata.TotalTokenCount,
		}
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: NewTextContent(content)},
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}, nil
}

// ChatCompletionStream implements StreamProvider — converts Gemini SSE to OpenAI SSE format.
func (p *GeminiProvider) ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	// Build Gemini request (same as non-streaming)
	gReq := geminiRequest{}
	gc := &geminiGenerationConfig{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.Stop,
	}
	gReq.GenerationConfig = gc

	for _, msg := range req.Messages {
		text, err := msg.TextContent()
		if err != nil {
			return nil, fmt.Errorf("gemini adapter only supports text content: %w", err)
		}
		if msg.Role == "system" {
			gReq.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: text}},
			}
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role != "user" && role != "model" {
			return nil, fmt.Errorf("gemini does not support %q messages", msg.Role)
		}
		gReq.Contents = append(gReq.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: text}},
		})
	}
	if len(gReq.Contents) == 0 {
		return nil, fmt.Errorf("gemini requires at least one non-system message")
	}

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	// Use streamGenerateContent with alt=sse for SSE format
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s",
		req.Model, p.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse Gemini SSE and convert to OpenAI SSE format
	chunkID := fmt.Sprintf("gemini-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	usage := &Usage{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	firstChunk := true
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var gResp geminiResponse
		if err := json.Unmarshal([]byte(data), &gResp); err != nil {
			return usage, fmt.Errorf("decode gemini stream event: %w", err)
		}

		// Extract text from candidates
		text := ""
		finishReason := ""
		if len(gResp.Candidates) > 0 {
			for _, part := range gResp.Candidates[0].Content.Parts {
				text += part.Text
			}
			if gResp.Candidates[0].FinishReason == "STOP" {
				finishReason = "stop"
			} else if gResp.Candidates[0].FinishReason == "MAX_TOKENS" {
				finishReason = "length"
			}
		}

		// Update usage from last chunk
		if gResp.UsageMetadata != nil {
			usage.PromptTokens = gResp.UsageMetadata.PromptTokenCount
			usage.CompletionTokens = gResp.UsageMetadata.CandidatesTokenCount
			usage.TotalTokens = gResp.UsageMetadata.TotalTokenCount
		}

		// Emit first chunk with role
		if firstChunk {
			WriteSSEChunk(w, StreamChunk{
				ID: chunkID, Object: "chat.completion.chunk",
				Created: created, Model: req.Model,
				Choices: []StreamChoice{{
					Index: 0,
					Delta: StreamDelta{Role: "assistant"},
				}},
			})
			firstChunk = false
		}

		// Emit content chunk
		if text != "" {
			chunk := StreamChunk{
				ID: chunkID, Object: "chat.completion.chunk",
				Created: created, Model: req.Model,
				Choices: []StreamChoice{{
					Index: 0,
					Delta: StreamDelta{Content: text},
				}},
			}
			WriteSSEChunk(w, chunk)
		}

		// Emit finish chunk if done
		if finishReason != "" {
			WriteSSEChunk(w, StreamChunk{
				ID: chunkID, Object: "chat.completion.chunk",
				Created: created, Model: req.Model,
				Choices: []StreamChoice{{
					Index:        0,
					Delta:        StreamDelta{},
					FinishReason: &finishReason,
				}},
			})
		}
	}

	WriteSSEDone(w)

	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("reading gemini stream: %w", err)
	}

	return usage, nil
}
