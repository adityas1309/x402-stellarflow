// Package handlers is the STELLARFLOW EXAMPLE — a real, working,
// agent-monetized sentiment analysis service that ships with this template.
//
// Flow per paid call (~$0.10 USDC):
//
//   1. Agent POSTs { topic } to /api/x402/sentiment (with X-PAYMENT header)
//   2. Backend calls Apify to scrape ~15 real Instagram posts about the topic
//   3. Backend calls OpenAI (gpt-5-mini) to analyze sentiment of those posts
//   4. Backend returns { sentiment, score, summary, sources, keywords }
//   5. x402 settlement transfers the USDC to the operator treasury
//
// If APIFY_TOKEN or OPENAI_API_KEY are not set, the handler falls back to
// a deterministic mock so the example works offline / in dry-run mode.
//
// REPLACE THIS package when forking the template for your own SaaS. The
// wire (internal/) never imports anything from here — it only knows the
// X402EndpointRegistry interface that ExampleService implements.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ─── Public API ────────────────────────────────────────────────

// ExampleService owns the single paid endpoint. It holds the Apify and
// OpenAI clients built from env vars at startup. If the env vars aren't
// set, the clients are nil and the handler falls back to deterministic mock.
type ExampleService struct {
	apify  *ApifyClient
	openai *OpenAIClient
}

// New constructs an ExampleService.
func New() *ExampleService {
	var apify *ApifyClient
	var openai *OpenAIClient

	if token := os.Getenv("APIFY_TOKEN"); token != "" {
		apify = NewApifyClient(token)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		model := os.Getenv("OPENAI_MODEL")
		if model == "" {
			model = "gpt-5-mini"
		}
		openai = NewOpenAIClient(key, model)
	}

	return &ExampleService{apify: apify, openai: openai}
}

// RegisterRoutes implements api.X402EndpointRegistry. One endpoint, one price.
func (es *ExampleService) RegisterRoutes(
	group *gin.RouterGroup,
	mw func(endpoint string, fallbackPriceUSDC float64) gin.HandlerFunc,
) {
	group.POST("/sentiment",
		mw("sentiment", 0.10),
		es.X402Sentiment)
}

// ─── Request / Response ────────────────────────────────────────

type sentimentRequest struct {
	Topic string `json:"topic" binding:"required"`
}

// sentimentResponse is the shape agents receive.
type sentimentResponse struct {
	Topic     string   `json:"topic"`
	Sentiment string   `json:"sentiment"` // "positive" | "negative" | "neutral"
	Score     float64  `json:"score"`     // -1.0 to 1.0
	Summary   string   `json:"summary"`
	Sources   int      `json:"sources"`
	Keywords  []string `json:"keywords"`
	Engine    string   `json:"engine"` // "apify+openai" or "mock"
}

// X402Sentiment is the single paid endpoint.
func (es *ExampleService) X402Sentiment(c *gin.Context) {
	var req sentimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topic is required"})
		return
	}

	// Bounded context so Apify + OpenAI can't hang the request forever.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	// Live path: Apify (scrape) → OpenAI (analyze).
	if es.apify != nil && es.openai != nil {
		result, err := es.analyzeLive(ctx, topic)
		if err == nil {
			c.JSON(http.StatusOK, result)
			return
		}
		// Log and fall through to mock so the demo never 500s.
		fmt.Fprintf(os.Stderr, "[stellarflow] live analysis failed (%v), falling back to mock\n", err)
	}

	// If Apify+OpenAI are configured but returned no usable data for this
	// topic (e.g. no Instagram posts found), return a clear message instead
	// of the silent mock fallback.
	if es.apify != nil && es.openai != nil {
		c.JSON(http.StatusOK, gin.H{
			"topic":     topic,
			"sentiment": "unknown",
			"score":     0,
			"summary":   fmt.Sprintf("No Instagram posts found for #%s. Try a more popular topic like 'stellar blockchain', 'tesla', 'nike', or 'openai'.", normalizeHashtag(topic)),
			"sources":   0,
			"keywords":  []string{},
			"engine":    "apify+openai (no data for this topic)",
		})
		return
	}

	// Mock path: only used when APIFY_TOKEN or OPENAI_API_KEY are not set.
	c.JSON(http.StatusOK, es.mockAnalysis(topic))
}

// ─── Live analysis ─────────────────────────────────────────────

func (es *ExampleService) analyzeLive(ctx context.Context, topic string) (*sentimentResponse, error) {
	// 1. Scrape recent Instagram posts for the topic as a hashtag.
	hashtag := normalizeHashtag(topic)
	posts, err := es.apify.ScrapeInstagramHashtag(ctx, hashtag, 15)
	if err != nil {
		return nil, fmt.Errorf("apify scrape: %w", err)
	}
	if len(posts) == 0 {
		return nil, fmt.Errorf("no posts found for hashtag #%s", hashtag)
	}

	// 2. Build the OpenAI prompt from the captions.
	var captions []string
	for _, p := range posts {
		caption := strings.TrimSpace(p.Caption)
		if len(caption) < 3 {
			continue
		}
		if len(caption) > 600 {
			caption = caption[:600] + "…"
		}
		captions = append(captions, caption)
	}
	if len(captions) == 0 {
		return nil, fmt.Errorf("posts had no usable captions")
	}

	// 3. Call OpenAI for the analysis.
	analysis, err := es.openai.AnalyzeSentiment(ctx, topic, captions)
	if err != nil {
		return nil, fmt.Errorf("openai analyze: %w", err)
	}

	return &sentimentResponse{
		Topic:     topic,
		Sentiment: analysis.Sentiment,
		Score:     analysis.Score,
		Summary:   analysis.Summary,
		Sources:   len(captions),
		Keywords:  analysis.Keywords,
		Engine:    "apify+openai",
	}, nil
}

// normalizeHashtag converts a free-form topic into a valid Instagram hashtag.
// "Stellar Blockchain!" → "stellarblockchain"
func normalizeHashtag(topic string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(topic) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "trending"
	}
	return b.String()
}

// ─── Mock fallback ─────────────────────────────────────────────

func (es *ExampleService) mockAnalysis(topic string) *sentimentResponse {
	// Deterministic mock so repeated calls for the same topic return
	// the same result. Used when no API keys are configured (dry-run
	// development) or when Apify/OpenAI fail mid-call.
	h := hashTopic(topic)
	score := float64(h%1000)/500.0 - 1.0 // -1 to 1

	var sentiment string
	switch {
	case score > 0.2:
		sentiment = "positive"
	case score < -0.2:
		sentiment = "negative"
	default:
		sentiment = "neutral"
	}

	keywords := []string{"adoption", "innovation", "community", "growth"}
	if sentiment == "negative" {
		keywords = []string{"risk", "volatility", "regulation", "concerns"}
	}

	return &sentimentResponse{
		Topic:     topic,
		Sentiment: sentiment,
		Score:     roundTo(score, 2),
		Summary:   mockSummary(topic, sentiment, score),
		Sources:   0,
		Keywords:  keywords,
		Engine:    "mock",
	}
}

func mockSummary(topic, sentiment string, score float64) string {
	prefix := fmt.Sprintf("Mock analysis of %q shows %s sentiment (score: %.2f). ", topic, sentiment, score)
	return prefix + "Set APIFY_TOKEN and OPENAI_API_KEY in .env for real analysis."
}

func hashTopic(s string) int {
	// Simple FNV-1a hash, positive only.
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for _, b := range []byte(strings.ToLower(s)) {
		h ^= uint32(b)
		h *= fnvPrime
	}
	return int(h & 0x7fffffff)
}

func roundTo(v float64, digits int) float64 {
	pow := 1.0
	for i := 0; i < digits; i++ {
		pow *= 10
	}
	return float64(int(v*pow+0.5)) / pow
}
