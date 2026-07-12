package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Summarizer calls an OpenAI-compatible API to generate digest summaries.
type Summarizer struct {
	endpoint   string
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// Result holds the output of a summarization call.
type Result struct {
	Summary string
	Model   string
	Usage   Usage
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func New(endpoint, apiKey, model string, maxTokens int) *Summarizer {
	return &Summarizer{
		endpoint:   endpoint,
		apiKey:     apiKey,
		model:      model,
		maxTokens:  maxTokens,
		httpClient: &http.Client{},
	}
}

const defaultPrompt = `You are a helpful assistant that summarizes YouTube video transcripts.
You will receive transcripts from multiple videos published in a recent time window.
Provide a concise digest covering:
1. Main topics and themes across all videos
2. Key points and takeaways
3. Any notable quotes or statements
4. Connections or common threads between videos

Group related content together. Mention which video each point comes from.`

func (s *Summarizer) Summarize(ctx context.Context, transcriptText string, systemPrompt string) (*Result, error) {
	if systemPrompt == "" {
		systemPrompt = defaultPrompt
	}

	reqBody := chatRequest{
		Model: s.model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: transcriptText},
		},
		MaxTokens: s.maxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := s.endpoint + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in API response")
	}

	return &Result{
		Summary: chatResp.Choices[0].Message.Content,
		Model:   chatResp.Model,
		Usage:   chatResp.Usage,
	}, nil
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}
