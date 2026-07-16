package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func providerResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testMessages() []Message {
	return []Message{
		{Role: "system", Content: NewTextContent("Be concise.")},
		{Role: "user", Content: NewTextContent("Hello")},
	}
}

func TestOpenAIChatCompletionForwardsRequest(t *testing.T) {
	provider := NewOpenAIProvider(OpenAIConfig{
		Name:    "openai",
		BaseURL: "https://provider.example/",
		APIKey:  "test-key",
		Models:  []string{"gpt-test"},
	})
	provider.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.String() != "https://provider.example/v1/chat/completions" {
			t.Fatalf("URL = %s", req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}

		var body ChatRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "gpt-test" || len(body.Messages) != 2 {
			t.Fatalf("unexpected request: %#v", body)
		}

		return providerResponse(http.StatusOK, `{
			"id":"chat-1",
			"object":"chat.completion",
			"created":1,
			"model":"gpt-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`), nil
	})}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Model:    "gpt-test",
		Messages: testMessages(),
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	text, err := resp.Choices[0].Message.TextContent()
	if err != nil {
		t.Fatalf("TextContent() error = %v", err)
	}
	if text != "Hi" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestOpenAIPassthroughStreamForwardsSSEAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body ChatRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if !body.Stream {
			t.Errorf("stream = false, want true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chunk-1\",\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	rec := httptest.NewRecorder()
	usage, err := OpenAIPassthroughStream(context.Background(), server.URL, "test-key", ChatRequest{
		Model:    "gpt-test",
		Messages: testMessages(),
	}, rec)
	if err != nil {
		t.Fatalf("OpenAIPassthroughStream() error = %v", err)
	}
	if usage == nil || usage.TotalTokens != 3 {
		t.Fatalf("usage = %#v", usage)
	}
	if got := rec.Body.String(); !strings.Contains(got, "data: [DONE]") || !strings.Contains(got, `"id":"chunk-1"`) {
		t.Fatalf("unexpected SSE body: %q", got)
	}
}

func TestAnthropicChatCompletionConvertsFormats(t *testing.T) {
	provider := NewAnthropicProvider("anthropic-key")
	provider.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("URL = %s", req.URL)
		}
		if req.Header.Get("x-api-key") != "anthropic-key" {
			t.Fatalf("missing Anthropic API key")
		}
		if req.Header.Get("anthropic-version") != anthropicAPIVersion {
			t.Fatalf("anthropic-version = %q", req.Header.Get("anthropic-version"))
		}

		var body anthropicRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.System != "Be concise." || len(body.Messages) != 1 || body.Messages[0].Content != "Hello" {
			t.Fatalf("unexpected Anthropic request: %#v", body)
		}

		return providerResponse(http.StatusOK, `{
			"id":"msg-1",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"Hello "},{"type":"text","text":"back"}],
			"model":"claude-sonnet-4-6",
			"stop_reason":"max_tokens",
			"usage":{"input_tokens":4,"output_tokens":2}
		}`), nil
	})}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: testMessages(),
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	text, _ := resp.Choices[0].Message.TextContent()
	if text != "Hello back" || resp.Choices[0].FinishReason != "length" || resp.Usage.TotalTokens != 6 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestGeminiChatCompletionConvertsFormats(t *testing.T) {
	provider := NewGeminiProvider("gemini-key")
	provider.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1beta/models/gemini-2.0-flash:generateContent" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if req.URL.Query().Get("key") != "gemini-key" {
			t.Fatalf("missing Gemini API key")
		}

		var body geminiRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.SystemInstruction == nil || body.SystemInstruction.Parts[0].Text != "Be concise." {
			t.Fatalf("unexpected system instruction: %#v", body.SystemInstruction)
		}
		if len(body.Contents) != 1 || body.Contents[0].Role != "user" {
			t.Fatalf("unexpected Gemini contents: %#v", body.Contents)
		}

		return providerResponse(http.StatusOK, `{
			"candidates":[{"content":{"role":"model","parts":[{"text":"Hi"}]},"finishReason":"MAX_TOKENS"}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}
		}`), nil
	})}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Model:    "gemini-2.0-flash",
		Messages: testMessages(),
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	text, _ := resp.Choices[0].Message.TextContent()
	if text != "Hi" || resp.Choices[0].FinishReason != "length" || resp.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestCohereChatCompletionConvertsHistory(t *testing.T) {
	provider := NewCohereProvider("cohere-key")
	provider.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer cohere-key" {
			t.Fatalf("missing Cohere API key")
		}

		var body cohereRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Preamble != "Be concise." || body.Message != "Latest question" {
			t.Fatalf("unexpected Cohere request: %#v", body)
		}
		if len(body.ChatHistory) != 2 || body.ChatHistory[0].Role != "USER" || body.ChatHistory[1].Role != "CHATBOT" {
			t.Fatalf("unexpected Cohere history: %#v", body.ChatHistory)
		}

		return providerResponse(http.StatusOK, `{
			"response_id":"cohere-1",
			"text":"Answer",
			"finish_reason":"COMPLETE",
			"meta":{"tokens":{"input_tokens":6,"output_tokens":2}}
		}`), nil
	})}

	resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Model: "command-r",
		Messages: []Message{
			{Role: "system", Content: NewTextContent("Be concise.")},
			{Role: "user", Content: NewTextContent("Earlier question")},
			{Role: "assistant", Content: NewTextContent("Earlier answer")},
			{Role: "user", Content: NewTextContent("Latest question")},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	text, _ := resp.Choices[0].Message.TextContent()
	if text != "Answer" || resp.Usage.TotalTokens != 8 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestCohereChatCompletionRequiresUserMessage(t *testing.T) {
	provider := NewCohereProvider("cohere-key")
	_, err := provider.ChatCompletion(context.Background(), ChatRequest{
		Model: "command-r",
		Messages: []Message{
			{Role: "system", Content: NewTextContent("Be concise.")},
			{Role: "assistant", Content: NewTextContent("Hello")},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires at least one user message") {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
}
