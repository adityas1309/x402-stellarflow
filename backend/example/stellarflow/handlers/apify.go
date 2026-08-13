package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ApifyClient is a minimal HTTP wrapper for the Apify Instagram hashtag scraper.
// We use the `run-sync-get-dataset-items` endpoint which blocks until the
// actor finishes, avoiding the polling dance the async endpoints require.
type ApifyClient struct {
	token string
	http  *http.Client
}

const (
	apifyBaseURL          = "https://api.apify.com/v2/acts"
	apifyInstagramActorID = "apify~instagram-scraper"
)

// NewApifyClient builds a client ready to call the Apify API.
func NewApifyClient(token string) *ApifyClient {
	return &ApifyClient{
		token: token,
		http:  &http.Client{Timeout: 50 * time.Second},
	}
}

// ApifyPost is the subset of fields we care about from an Instagram post
// returned by the Apify actor.
type ApifyPost struct {
	Caption       string `json:"caption"`
	LikesCount    int    `json:"likesCount"`
	CommentsCount int    `json:"commentsCount"`
	URL           string `json:"url"`
	Timestamp     string `json:"timestamp"`
}

// ScrapeInstagramHashtag scrapes recent Instagram posts for the given hashtag
// and returns the top `limit` of them. Empty strings are pre-filtered.
func (c *ApifyClient) ScrapeInstagramHashtag(ctx context.Context, hashtag string, limit int) ([]ApifyPost, error) {
	if limit <= 0 {
		limit = 10
	}

	input := map[string]interface{}{
		"directUrls":    []string{fmt.Sprintf("https://www.instagram.com/explore/tags/%s/", hashtag)},
		"resultsType":   "posts",
		"resultsLimit":  limit,
		"searchType":    "hashtag",
		"searchLimit":   1,
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	url := fmt.Sprintf("%s/%s/run-sync-get-dataset-items?token=%s",
		apifyBaseURL, apifyInstagramActorID, c.token)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
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
		return nil, fmt.Errorf("apify returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var posts []ApifyPost
	if err := json.Unmarshal(respBody, &posts); err != nil {
		return nil, fmt.Errorf("parse posts: %w (body=%s)", err, truncate(string(respBody), 300))
	}

	return posts, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
