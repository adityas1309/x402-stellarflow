package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	db "github.com/your-org/stellarflow/internal/db/sqlc"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// handler_agent.go — agent wallet provisioning endpoint.
//
// Exposes POST /api/agent/sponsor for the MCP server's `init` command.
// The endpoint creates a fresh Stellar account on testnet
// funded with 2 XLM from the operator's treasury, so the agent only has
// to deal with USDC funding from there on out.
//
// No auth on this endpoint for the hackathon — anyone can request to be
// sponsored. Defenses against abuse:
//   1. In-process rate limit: max 5 sponsorships per IP per hour
//   2. Treasury balance guard: refuses if treasury XLM < 5
//   3. Idempotent: if the address already exists on the network, returns
//      success without spending more XLM
//   4. Hard cap: SponsorAmountXLM = 2 XLM (~$0.27 worst case per call)

// sponsorRateLimit is a tiny in-memory token bucket per source IP.
// Reset on process restart. Adequate for hackathon, NOT for production.
type sponsorRateLimit struct {
	mu      sync.Mutex
	buckets map[string][]time.Time // ip → timestamps of recent grants
}

func newSponsorRateLimit() *sponsorRateLimit {
	return &sponsorRateLimit{buckets: make(map[string][]time.Time)}
}

const (
	sponsorRateMaxPerHour = 5
	sponsorRateWindow     = time.Hour
	sponsorMinTreasuryXLM = 5.0
)

// allow returns true if this IP is under the rate limit, otherwise false.
// Records the grant on success.
func (r *sponsorRateLimit) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-sponsorRateWindow)

	// Drop stale entries.
	kept := make([]time.Time, 0, len(r.buckets[ip]))
	for _, ts := range r.buckets[ip] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= sponsorRateMaxPerHour {
		r.buckets[ip] = kept
		return false
	}

	r.buckets[ip] = append(kept, now)
	return true
}

// Lazy-singleton so the rate limiter survives across requests but doesn't
// pollute the Server struct (it's a hackathon-only defense).
var sponsorLimiter = newSponsorRateLimit()

type sponsorRequest struct {
	Address string `json:"address" binding:"required"`
}

type sponsorResponse struct {
	Address           string `json:"address"`
	TxHash            string `json:"tx_hash,omitempty"` // empty if account already existed
	AlreadyExisted    bool   `json:"already_existed"`
	FundedXLM         string `json:"funded_xlm"`
	NetworkPassphrase string `json:"network_passphrase"`
	NextStep          string `json:"next_step"`
}

// sponsorAgent funds a fresh Stellar account so an agent can start using
// x402 endpoints. See the file header for the security defenses.
func (s *Server) sponsorAgent(ctx *gin.Context) {
	if s.sponsor == nil {
		ctx.JSON(http.StatusServiceUnavailable, errorResponse(
			"sponsoring not configured: server has no X402_TREASURY_SECRET",
			"sponsor_disabled",
		))
		return
	}

	// Rate limit by client IP.
	ip := ctx.ClientIP()
	if !sponsorLimiter.allow(ip) {
		ctx.JSON(http.StatusTooManyRequests, errorResponse(
			"sponsor rate limit exceeded (max 5 per hour per IP)",
			"rate_limited",
		))
		return
	}

	var req sponsorRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err.Error(), "validation_error"))
		return
	}

	// Treasury balance guard: refuse if we're running low.
	balanceStr, err := s.sponsor.TreasuryBalance(ctx)
	if err != nil {
		logrus.WithError(err).Warn("sponsor: failed to read treasury balance")
		ctx.JSON(http.StatusInternalServerError, errorResponse(
			"failed to check treasury balance",
			"internal_error",
		))
		return
	}
	if balanceFloat, perr := strconv.ParseFloat(balanceStr, 64); perr == nil && balanceFloat < sponsorMinTreasuryXLM {
		ctx.JSON(http.StatusServiceUnavailable, errorResponse(
			"treasury low on XLM, sponsoring temporarily disabled",
			"treasury_low",
		))
		return
	}

	txHash, err := s.sponsor.SponsorAgent(ctx, req.Address)
	if err != nil {
		logrus.WithError(err).WithField("address", req.Address).Warn("sponsor failed")
		ctx.JSON(http.StatusBadGateway, errorResponse(err.Error(), "sponsor_failed"))
		return
	}

	alreadyExisted := txHash == ""
	logrus.WithFields(logrus.Fields{
		"address":         req.Address,
		"tx_hash":         txHash,
		"already_existed": alreadyExisted,
		"client_ip":       ip,
	}).Info("sponsored agent account")

	ctx.JSON(http.StatusOK, sponsorResponse{
		Address:           req.Address,
		TxHash:            txHash,
		AlreadyExisted:    alreadyExisted,
		FundedXLM:         "2.0000000",
		NetworkPassphrase: networkPassphraseFor(s.config.X402Network),
		NextStep:          "Establish the USDC trustline on this account, then send USDC to fund agent calls.",
	})
}

// networkPassphraseFor maps the env-style network identifier to the actual
// passphrase string the SDK expects.
func networkPassphraseFor(envNetwork string) string {
	return "Test SDF Network ; September 2015"
}

// ─────────────────────────────────────────────────────────────────────────
// PER-AGENT USAGE DASHBOARD
// ─────────────────────────────────────────────────────────────────────────

// agentUsageResponse is the JSON shape for GET /api/agent/me/usage.
type agentUsageResponse struct {
	Wallet      walletInfo                       `json:"wallet"`
	Summary     interface{}                      `json:"summary"`     // db.AgentUsageSummary
	ByEndpoint  []db.AgentEndpointBreakdown      `json:"by_endpoint"` // breakdown
	RecentCalls []FeedEvent                      `json:"recent_calls"`
}

type walletInfo struct {
	Address string `json:"address"`
	Network string `json:"network"`
}

// agentUsage returns the calls and aggregate stats for the wallet that
// is currently authenticated. The wallet G-address is parsed from the
// SEP-10 token's email field, which is set by handler_sep10.go to the
// synthetic form `wallet+G...@stellar.local`.
func (s *Server) agentUsage(ctx *gin.Context) {
	payload := getAuthPayload(ctx)
	if payload == nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse("missing auth payload", "unauthorized"))
		return
	}

	walletAddr, ok := walletAddressFromEmail(payload.Email)
	if !ok {
		ctx.JSON(http.StatusForbidden, errorResponse(
			"this token is not bound to a Stellar wallet — sign in via SEP-10",
			"not_a_wallet_token",
		))
		return
	}

	summary, err := s.store.GetAgentUsageSummary(ctx, walletAddr)
	if err != nil {
		logrus.WithError(err).WithField("wallet", walletAddr).Warn("agent usage: load summary failed")
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load usage summary", "internal_error"))
		return
	}

	breakdown, err := s.store.GetAgentEndpointBreakdown(ctx, walletAddr)
	if err != nil {
		logrus.WithError(err).WithField("wallet", walletAddr).Warn("agent usage: load breakdown failed")
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load endpoint breakdown", "internal_error"))
		return
	}

	calls, err := s.store.ListRecentAgentCallsByPayer(ctx, walletAddr, 25)
	if err != nil {
		logrus.WithError(err).WithField("wallet", walletAddr).Warn("agent usage: load calls failed")
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load recent calls", "internal_error"))
		return
	}

	feedEvents := make([]FeedEvent, 0, len(calls))
	for _, c := range calls {
		feedEvents = append(feedEvents, FeedEvent{
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

	ctx.JSON(http.StatusOK, agentUsageResponse{
		Wallet: walletInfo{
			Address: walletAddr,
			Network: s.config.X402Network,
		},
		Summary:     summary,
		ByEndpoint:  breakdown,
		RecentCalls: feedEvents,
	})
}

// walletAddressFromEmail parses the synthetic SEP-10 email format
// `wallet+G...@stellar.local` and returns the embedded G-address. Returns
// false if the email is not in the expected format (e.g. an old email/
// password account from before SEP-10 was added).
func walletAddressFromEmail(email string) (string, bool) {
	const prefix = "wallet+"
	const suffix = "@stellar.local"
	if !strings.HasPrefix(email, prefix) || !strings.HasSuffix(email, suffix) {
		return "", false
	}
	addr := strings.TrimSuffix(strings.TrimPrefix(email, prefix), suffix)
	if len(addr) != 56 || addr[0] != 'G' {
		return "", false
	}
	return addr, true
}
