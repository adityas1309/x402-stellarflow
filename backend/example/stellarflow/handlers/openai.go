package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient is a minimal HTTP wrapper for the OpenAI Chat Completions API.
// We only use one endpoint (POST /v1/chat/completions) with JSON mode.
type OpenAIClient struct {
	apiKey string
	model  string
	http   *http.Client
}

const openAIBaseURL = "https://api.openai.com/v1/chat/completions"

// NewOpenAIClient builds a client using the given API key and model name.
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// SentimentAnalysis is the structured result the model is asked to produce.
type SentimentAnalysis struct {
	Sentiment string   `json:"sentiment"` // "positive" | "negative" | "neutral"
	Score     float64  `json:"score"`     // -1.0 to 1.0
	Summary   string   `json:"summary"`
	Keywords  []string `json:"keywords"`
}

// AnalyzeSentiment asks the model to read a batch of social media captions
// about a topic and return a structured sentiment analysis. JSON mode is
// enforced so the response parses cleanly.
func (c *OpenAIClient) AnalyzeSentiment(ctx context.Context, topic string, captions []string) (*SentimentAnalysis, error) {
	if len(captions) == 0 {
		return nil, fmt.Errorf("no captions to analyze")
	}

	// Build a compact prompt. The model needs to see all the captions
	// but we don't want to blow the context window on very long ones —
	// the handler already truncates to 600 chars each before calling.
	var sb strings.Builder
	sb.WriteString("Analyze the overall public sentiment about the topic: \"")
	sb.WriteString(topic)
	sb.WriteString("\"\n\n")
	sb.WriteString("Below are recent social media captions mentioning this topic:\n\n")
	for i, cap := range captions {
		sb.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, cap))
	}
	sb.WriteString("\nProduce a JSON object with this exact shape:\n")
	sb.WriteString(`{
  "sentiment": "positive" | "negative" | "neutral",
  "score": <number between -1.0 and 1.0>,
  "summary": "<2-3 sentence human-readable summary>",
  "keywords": ["<3-5 recurring themes from the captions>"]
}`)
	sb.WriteString("\n\nReturn ONLY the JSON, no prose, no markdown.")

	requestBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a concise sentiment analysis engine. Always respond with valid JSON matching the requested schema.",
			},
			{
				"role":    "user",
				"content": sb.String(),
			},
		},
		"response_format": map[string]string{"type": "json_object"},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openAIBaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	// OpenAI chat completions wrapper
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return nil, fmt.Errorf("parse completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no choices in completion response")
	}

	// The message.content is a JSON string (because we asked for json_object).
	contentJSON := completion.Choices[0].Message.Content
	var analysis SentimentAnalysis
	if err := json.Unmarshal([]byte(contentJSON), &analysis); err != nil {
		return nil, fmt.Errorf("parse sentiment JSON: %w (content=%s)", err, truncate(contentJSON, 300))
	}

	// Sanity clamp on score.
	if analysis.Score < -1 {
		analysis.Score = -1
	} else if analysis.Score > 1 {
		analysis.Score = 1
	}
	if analysis.Sentiment == "" {
		analysis.Sentiment = "neutral"
	}

	return &analysis, nil
}
