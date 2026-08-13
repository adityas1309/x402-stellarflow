package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	db "github.com/your-org/stellarflow/internal/db/sqlc"

	"github.com/gin-gonic/gin"
)

// Operator-facing endpoints (no SEP-10 auth required for the hackathon demo —
// in production they would all be wallet-authenticated and scoped to the
// caller's org_id).
//
//   GET /api/operator/feed       — SSE live stream of new agent calls
//   GET /api/operator/recent     — last N agent calls (REST, for initial load)
//   GET /api/operator/revenue    — revenue summary for the last 24h (or ?since=)
//   GET /api/operator/top        — top endpoints + top payers leaderboards
//   GET /api/catalog             — public catalog of x402 endpoints + pricing
//
// All operator endpoints are scoped to demoOrgID for now (single tenant).

// ─────────────────────────────────────────────────────────────────────────
// LIVE FEED (SSE)
// ─────────────────────────────────────────────────────────────────────────

func (s *Server) operatorFeed(ctx *gin.Context) {
	// SSE headers
	ctx.Writer.Header().Set("Content-Type", "text/event-stream")
	ctx.Writer.Header().Set("Cache-Control", "no-cache")
	ctx.Writer.Header().Set("Connection", "keep-alive")
	ctx.Writer.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	ctx.Writer.Flush()

	if s.feedHub == nil {
		ctx.SSEvent("error", gin.H{"message": "feed hub not initialized"})
		return
	}

	events, unsubscribe := s.feedHub.Subscribe()
	defer unsubscribe()

	// Send a hello so the client knows the connection is live.
	hello, _ := json.Marshal(gin.H{
		"type":            "hello",
		"subscriber_count": s.feedHub.SubscriberCount(),
		"now":              time.Now().Format(time.RFC3339),
	})
	fmt.Fprintf(ctx.Writer, "event: hello\ndata: %s\n\n", hello)
	ctx.Writer.Flush()

	// Heartbeat ticker so reverse proxies don't drop the connection.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	clientGone := ctx.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case <-heartbeat.C:
			fmt.Fprintf(ctx.Writer, ": keep-alive %d\n\n", time.Now().Unix())
			ctx.Writer.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(ctx.Writer, "event: agent_call\ndata: %s\n\n", data)
			ctx.Writer.Flush()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// RECENT CALLS (REST)
// ─────────────────────────────────────────────────────────────────────────

func (s *Server) operatorRecent(ctx *gin.Context) {
	limit := int32(50)
	if v := ctx.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}

	calls, err := s.store.ListRecentAgentCallsByOrg(ctx, demoOrgID, limit)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load calls", "internal_error"))
		return
	}

	out := make([]FeedEvent, 0, len(calls))
	for _, c := range calls {
		out = append(out, FeedEvent{
			ID:           c.ID,
			OrgID:        c.OrgID,
			PayerAddress: c.PayerAddress,
			Endpoint:     c.Endpoint,
			PriceUSDC:    c.PriceUSDC,
			Facilitator:  c.Facilitator,
			Status:       c.Status,
			ClientID:     c.ClientID.String,
			Reasoning:    c.Reasoning.String,
			TxHash:       c.TxHash.String,
			DurationMs:   int(c.DurationMs.Int32),
			ResponseSize: int(c.ResponseSize.Int32),
			CreatedAt:    c.CreatedAt,
		})
	}
	ctx.JSON(http.StatusOK, gin.H{"calls": out, "count": len(out)})
}

// ─────────────────────────────────────────────────────────────────────────
// REVENUE SUMMARY
// ─────────────────────────────────────────────────────────────────────────

func (s *Server) operatorRevenue(ctx *gin.Context) {
	since := time.Now().Add(-24 * time.Hour)
	if v := ctx.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}

	summary, err := s.store.GetRevenueSummaryByOrg(ctx, demoOrgID, since)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load revenue", "internal_error"))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"total_usdc":    summary.TotalUSDC,
		"total_calls":   summary.TotalCalls,
		"unique_agents": summary.UniqueAgents,
		"since":         since.Format(time.RFC3339),
	})
}

// ─────────────────────────────────────────────────────────────────────────
// TOP ENDPOINTS / TOP PAYERS
// ─────────────────────────────────────────────────────────────────────────

func (s *Server) operatorTop(ctx *gin.Context) {
	since := time.Now().Add(-24 * time.Hour)
	if v := ctx.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}

	endpoints, err := s.store.GetTopEndpointsByOrg(ctx, demoOrgID, since, 10)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load top endpoints", "internal_error"))
		return
	}
	payers, err := s.store.GetTopPayersByOrg(ctx, demoOrgID, since, 10)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load top payers", "internal_error"))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"top_endpoints": endpoints,
		"top_payers":    payers,
		"since":         since.Format(time.RFC3339),
	})
}

// ─────────────────────────────────────────────────────────────────────────
// PRICING (operator only)
// ─────────────────────────────────────────────────────────────────────────

// listOperatorPricing returns ALL pricing rows for the operator (enabled +
// disabled), so the dashboard can render the full editable list.
func (s *Server) listOperatorPricing(ctx *gin.Context) {
	rows, err := s.store.ListX402PricingByOrg(ctx, demoOrgID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load pricing", "internal_error"))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"pricing": rows})
}

type upsertPricingRequest struct {
	Endpoint  string  `json:"endpoint" binding:"required"`
	PriceUSDC float64 `json:"price_usdc" binding:"gte=0"`
	Enabled   bool    `json:"enabled"`
}

// upsertOperatorPricing creates or updates the price for one endpoint.
// The dashboard sends one of these per row edit. There is no auth on this
// endpoint for the hackathon — production would gate it behind a SEP-10
// PASETO token scoped to the operator org.
func (s *Server) upsertOperatorPricing(ctx *gin.Context) {
	var req upsertPricingRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err.Error(), "validation_error"))
		return
	}

	row, err := s.store.UpsertX402Pricing(ctx, db.UpsertX402PricingParams{
		OrgID:     demoOrgID,
		Endpoint:  req.Endpoint,
		PriceUSDC: req.PriceUSDC,
		Enabled:   req.Enabled,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to upsert pricing", "internal_error"))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"pricing": row})
}

// ─────────────────────────────────────────────────────────────────────────
// PUBLIC CATALOG
// ─────────────────────────────────────────────────────────────────────────

// catalogEndpoint is one row in the public catalog. Designed to be machine-
// readable for agents that want to discover available services and prices.
type catalogEndpoint struct {
	Endpoint     string  `json:"endpoint"`
	Method       string  `json:"method"`
	Path         string  `json:"path"`
	PriceUSDC    float64 `json:"price_usdc"`
	Description  string  `json:"description"`
	Network      string  `json:"network"`
	PayTo        string  `json:"pay_to"`
	Asset        string  `json:"asset"`
	AssetCode    string  `json:"asset_code"`
}

// hard-coded for now: a real catalog would join x402_pricing with a
// per-endpoint description table. We keep this in code so adding a new
// endpoint requires touching exactly one place.
var endpointMetadata = map[string]struct {
	Description string
	Method      string
	Path        string
}{
	"posting_heatmap":     {"Best (day, hour) slots ranked by engagement, computed over the last ~30 scraped posts.", "POST", "/api/x402/posting_heatmap"},
	"trending_hashtags":   {"Top hashtags by engagement vs baseline, with breakout flags.", "POST", "/api/x402/trending_hashtags"},
	"breakout_posts":      {"Posts with engagement significantly above the median.", "POST", "/api/x402/breakout_posts"},
	"compare_competitors": {"Side-by-side metrics for multiple competitors.", "POST", "/api/x402/compare_competitors"},
	"competitor_snapshot": {"Full profile + headline metrics for a single competitor.", "POST", "/api/x402/competitor_snapshot"},
}

func (s *Server) publicCatalog(ctx *gin.Context) {
	pricing, err := s.store.ListEnabledX402PricingByOrg(ctx, demoOrgID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load catalog", "internal_error"))
		return
	}

	out := make([]catalogEndpoint, 0, len(pricing))
	for _, p := range pricing {
		meta, ok := endpointMetadata[p.Endpoint]
		if !ok {
			meta.Description = "(no description)"
			meta.Method = "POST"
			meta.Path = "/api/x402/" + p.Endpoint
		}
		out = append(out, catalogEndpoint{
			Endpoint:    p.Endpoint,
			Method:      meta.Method,
			Path:        meta.Path,
			PriceUSDC:   p.PriceUSDC,
			Description: meta.Description,
			Network:     s.config.X402Network,
			PayTo:       s.config.X402TreasuryAddress,
			Asset:       s.config.X402USDCIssuer,
			AssetCode:   s.config.X402USDCCode,
		})
	}

	ctx.JSON(http.StatusOK, gin.H{
		"catalog":   out,
		"operator":  walletShorthand(s.config.X402TreasuryAddress),
		"network":   s.config.X402Network,
		"facilitator_url": s.config.X402FacilitatorURL,
	})
}
