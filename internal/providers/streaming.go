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
)

// StreamProvider extends Provider with SSE streaming capability.
type StreamProvider interface {
	Provider
	// ChatCompletionStream sends a streaming chat completion request.
	// It writes SSE events directly to w in OpenAI format.
	// Returns aggregated Usage for logging (may be nil if unavailable).
	ChatCompletionStream(ctx context.Context, req ChatRequest, w http.ResponseWriter) (*Usage, error)
}

// StreamChunk represents an OpenAI-format SSE streaming chunk.
type StreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// StreamChoice is a single choice in a streaming chunk.
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// StreamDelta represents the incremental content in a stream chunk.
type StreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// WriteSSE writes a single SSE data line and flushes.
func WriteSSE(w http.ResponseWriter, data []byte) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteSSEDone writes the [DONE] terminator.
func WriteSSEDone(w http.ResponseWriter) {
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteSSEChunk marshals a StreamChunk and writes it as SSE.
func WriteSSEChunk(w http.ResponseWriter, chunk StreamChunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	WriteSSE(w, data)
	return nil
}

// OpenAIPassthroughStream sends a streaming request to an OpenAI-compatible endpoint
// and passes through SSE events directly to the client.
// Returns aggregated Usage if available from the stream.
func OpenAIPassthroughStream(ctx context.Context, endpoint, apiKey string, req ChatRequest, w http.ResponseWriter) (*Usage, error) {
	return openAIPassthroughStreamWithClient(ctx, &http.Client{}, endpoint, apiKey, req, w)
}

func openAIPassthroughStreamWithClient(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	apiKey string,
	req ChatRequest,
	w http.ResponseWriter,
) (*Usage, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var usage *Usage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			WriteSSEDone(w)
			break
		}

		// Try to extract usage from chunk (some providers include it in last chunk)
		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err == nil && chunk.Usage != nil {
			usage = chunk.Usage
		}

		// Pass through as-is (preserves original provider format which is OpenAI-compatible)
		WriteSSE(w, []byte(data))
	}

	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("reading stream: %w", err)
	}

	return usage, nil
}
