// Smoke test for the StellarFlow sentiment analysis flow.
//
// Run with:
//   cd backend
//   go run ./cmd/test-stellarflow "stellar blockchain"
//
// Reads APIFY_TOKEN + OPENAI_API_KEY from the process env (source .env first)
// and calls the full analysis pipeline directly. Prints the result JSON.
// If either key is missing, falls back to the deterministic mock so you can
// still see the shape of the response.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	stellarflowhandlers "github.com/your-org/stellarflow/example/stellarflow/handlers"
	"github.com/gin-gonic/gin"
)

func main() {
	topic := "stellar blockchain"
	if len(os.Args) > 1 {
		topic = os.Args[1]
	}

	fmt.Printf("━━━ StellarFlow smoke test ━━━\n")
	fmt.Printf("Topic: %q\n", topic)
	fmt.Printf("APIFY_TOKEN:    %s\n", maskKey(os.Getenv("APIFY_TOKEN")))
	fmt.Printf("OPENAI_API_KEY: %s\n", maskKey(os.Getenv("OPENAI_API_KEY")))
	fmt.Printf("OPENAI_MODEL:   %s\n", defaultEmpty(os.Getenv("OPENAI_MODEL"), "gpt-5-mini"))
	fmt.Println()

	svc := stellarflowhandlers.New()

	// Build a fake gin.Context so we can call X402Sentiment directly without
	// spinning up an HTTP server, DB, or x402 middleware.
	gin.SetMode(gin.ReleaseMode)
	body, _ := json.Marshal(map[string]string{"topic": topic})
	req := httptest.NewRequest(http.MethodPost, "/api/x402/sentiment", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	start := time.Now()
	svc.X402Sentiment(c)
	elapsed := time.Since(start)

	fmt.Printf("Status: %d\n", rec.Code)
	fmt.Printf("Elapsed: %s\n", elapsed.Round(10*time.Millisecond))
	fmt.Println()
	fmt.Println("Response body:")
	printJSON(rec.Body.Bytes())
}

// ─── Helpers ────────────────────────────────────────────────────

func maskKey(k string) string {
	if k == "" {
		return "(not set — will use mock fallback)"
	}
	if len(k) <= 12 {
		return "(set)"
	}
	return k[:8] + "…" + k[len(k)-4:]
}

func defaultEmpty(s, fallback string) string {
	if s == "" {
		return fallback + " (default)"
	}
	return s
}

func printJSON(raw []byte) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		fmt.Println(string(raw))
		return
	}
	pretty, _ := json.MarshalIndent(v, "  ", "  ")
	fmt.Println("  " + string(pretty))
}
