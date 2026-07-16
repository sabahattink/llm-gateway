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

func TestAnthropicChatCompletionStreamConvertsEvents(t *testing.T) {
	provider := NewAnthropicProvider("anthropic-key")
	provider.streamClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body anthropicRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !body.Stream || body.System != "Be concise." || len(body.Messages) != 1 {
			t.Fatalf("unexpected Anthropic stream request: %#v", body)
		}

		return providerResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg-stream","model":"claude-sonnet-4-6","usage":{"input_tokens":4}}}`,
			"",
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			"",
			`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":2}}`,
			"",
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")), nil
	})}

	rec := httptest.NewRecorder()
	usage, err := provider.ChatCompletionStream(context.Background(), ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: testMessages(),
	}, rec)
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}
	if usage == nil || usage.PromptTokens != 4 || usage.CompletionTokens != 2 || usage.TotalTokens != 6 {
		t.Fatalf("usage = %#v", usage)
	}
	body := rec.Body.String()
	for _, expected := range []string{`"role":"assistant"`, `"content":"Hello"`, `"finish_reason":"length"`, "data: [DONE]"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream body missing %q: %s", expected, body)
		}
	}
}

func TestGeminiChatCompletionStreamConvertsEvents(t *testing.T) {
	provider := NewGeminiProvider("gemini-key")
	provider.streamClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1beta/models/gemini-2.0-flash:streamGenerateContent" ||
			req.URL.Query().Get("alt") != "sse" ||
			req.URL.Query().Get("key") != "gemini-key" {
			t.Fatalf("unexpected Gemini stream URL: %s", req.URL)
		}
		return providerResponse(http.StatusOK, strings.Join([]string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
			"",
			`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`,
			"",
		}, "\n")), nil
	})}

	rec := httptest.NewRecorder()
	usage, err := provider.ChatCompletionStream(context.Background(), ChatRequest{
		Model:    "gemini-2.0-flash",
		Messages: testMessages(),
	}, rec)
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", usage)
	}
	body := rec.Body.String()
	for _, expected := range []string{`"role":"assistant"`, `"content":"Hello"`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream body missing %q: %s", expected, body)
		}
	}
}

func TestNativeStreamsRejectMalformedEvents(t *testing.T) {
	tests := []struct {
		name string
		run  func(*http.Client) error
		want string
	}{
		{
			name: "anthropic",
			run: func(client *http.Client) error {
				provider := NewAnthropicProvider("key")
				provider.streamClient = client
				_, err := provider.ChatCompletionStream(context.Background(), ChatRequest{
					Model: "claude-sonnet-4-6", Messages: testMessages(),
				}, httptest.NewRecorder())
				return err
			},
			want: "decode anthropic stream event",
		},
		{
			name: "gemini",
			run: func(client *http.Client) error {
				provider := NewGeminiProvider("key")
				provider.streamClient = client
				_, err := provider.ChatCompletionStream(context.Background(), ChatRequest{
					Model: "gemini-2.0-flash", Messages: testMessages(),
				}, httptest.NewRecorder())
				return err
			},
			want: "decode gemini stream event",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return providerResponse(http.StatusOK, "data: {not-json}\n\n"), nil
			})}
			err := test.run(client)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGeminiRequiresConversationMessage(t *testing.T) {
	req := ChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{Role: "system", Content: NewTextContent("Be concise.")},
		},
	}
	provider := NewGeminiProvider("key")

	if _, err := provider.ChatCompletion(context.Background(), req); err == nil ||
		!strings.Contains(err.Error(), "requires at least one non-system message") {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if _, err := provider.ChatCompletionStream(context.Background(), req, httptest.NewRecorder()); err == nil ||
		!strings.Contains(err.Error(), "requires at least one non-system message") {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}
}

func TestCompatibleProviderEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		run      func(*http.Client) (*ChatResponse, error)
		stream   func(*http.Client, http.ResponseWriter) (*Usage, error)
	}{
		{
			name: "mistral", endpoint: "https://api.mistral.ai/v1/chat/completions",
			run: func(client *http.Client) (*ChatResponse, error) {
				provider := NewMistralProvider("test-key")
				provider.client = client
				return provider.ChatCompletion(context.Background(), ChatRequest{Model: "mistral-large", Messages: testMessages()})
			},
			stream: func(client *http.Client, w http.ResponseWriter) (*Usage, error) {
				provider := NewMistralProvider("test-key")
				provider.streamClient = client
				return provider.ChatCompletionStream(context.Background(), ChatRequest{Model: "mistral-large", Messages: testMessages()}, w)
			},
		},
		{
			name: "xai", endpoint: "https://api.x.ai/v1/chat/completions",
			run: func(client *http.Client) (*ChatResponse, error) {
				provider := NewXAIProvider("test-key")
				provider.client = client
				return provider.ChatCompletion(context.Background(), ChatRequest{Model: "grok-2", Messages: testMessages()})
			},
			stream: func(client *http.Client, w http.ResponseWriter) (*Usage, error) {
				provider := NewXAIProvider("test-key")
				provider.streamClient = client
				return provider.ChatCompletionStream(context.Background(), ChatRequest{Model: "grok-2", Messages: testMessages()}, w)
			},
		},
		{
			name: "perplexity", endpoint: "https://api.perplexity.ai/chat/completions",
			run: func(client *http.Client) (*ChatResponse, error) {
				provider := NewPerplexityProvider("test-key")
				provider.client = client
				return provider.ChatCompletion(context.Background(), ChatRequest{Model: "sonar-large", Messages: testMessages()})
			},
			stream: func(client *http.Client, w http.ResponseWriter) (*Usage, error) {
				provider := NewPerplexityProvider("test-key")
				provider.streamClient = client
				return provider.ChatCompletionStream(context.Background(), ChatRequest{Model: "sonar-large", Messages: testMessages()}, w)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestCount++
				if req.URL.String() != test.endpoint {
					t.Fatalf("URL = %s, want %s", req.URL, test.endpoint)
				}
				if req.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatalf("Authorization = %q", req.Header.Get("Authorization"))
				}

				var body ChatRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if requestCount == 1 {
					return providerResponse(http.StatusOK, `{
						"id":"chat-1","object":"chat.completion","model":"test",
						"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
						"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
					}`), nil
				}
				if !body.Stream {
					t.Fatal("stream request did not set stream=true")
				}
				return providerResponse(http.StatusOK,
					"data: {\"id\":\"chunk-1\",\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n",
				), nil
			})}

			resp, err := test.run(client)
			if err != nil || resp.Usage.TotalTokens != 2 {
				t.Fatalf("ChatCompletion() response=%#v error=%v", resp, err)
			}
			usage, err := test.stream(client, httptest.NewRecorder())
			if err != nil || usage == nil || usage.TotalTokens != 2 {
				t.Fatalf("ChatCompletionStream() usage=%#v error=%v", usage, err)
			}
			if requestCount != 2 {
				t.Fatalf("request count = %d, want 2", requestCount)
			}
		})
	}
}
