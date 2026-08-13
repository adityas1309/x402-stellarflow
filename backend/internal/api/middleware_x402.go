package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"

	db "github.com/your-org/stellarflow/internal/db/sqlc"
	"github.com/your-org/stellarflow/internal/x402"

	"github.com/gin-gonic/gin"
)

// x402 v2 protocol implementation (server side).
//
// On a request to a paid endpoint:
//
//   1. If the X-PAYMENT header is missing → 402 with PaymentRequired body.
//   2. Otherwise: base64-decode + JSON-parse the inner PaymentPayload.
//   3. Validate scheme/network/payTo/asset all match the requirements we
//      published. Mismatches → 402 with the specific error.
//   4. In dry-run mode (X402_DRY_RUN=true): trust the payload, extract a
//      payer address best-effort, run the inner handler.
//      In live mode: POST to facilitator /verify. If isValid=false → 402.
//      If isValid=true, run the inner handler.
//   5. After the inner handler runs successfully, in live mode POST to
//      facilitator /settle to actually execute the payment. The returned
//      tx hash is logged into agent_calls and surfaced in the live feed.
//   6. Always log the call to agent_calls (status=paid|failed) and publish
//      a FeedEvent so the operator dashboard updates in real time.

const (
	headerXPayment       = "X-Payment"
	x402SchemeExact      = "exact"
	x402Version          = 2
	clientIDHeader       = "X-StellarFlow-Client" // optional, e.g. "claude-code"
	reasoningHeader      = "X-StellarFlow-Reason" // optional agent reasoning text
	defaultMaxTimeoutSec = 30

	// Hackathon: single-tenant. Every paid call lands in the bootstrap org's
	// agent_calls. When we add multi-tenant catalogs, this resolves from
	// the URL path/subdomain.
	demoOrgID = int64(1)
)

// x402Context is what the middleware stashes on gin.Context for downstream
// handlers (the actual paid endpoint logic) to read via GetX402Context.
type x402Context struct {
	Endpoint     string
	PayerAddress string
	PriceUSDC    float64
	Facilitator  string // "dry-run" or "openzeppelin"
	ClientID     string
	Reasoning    string
	StartedAt    time.Time
}

const x402ContextKey = "x402"

// X402Middleware wraps a paid endpoint with x402 verification + agent_calls
// logging. The endpoint name is used both as the agent_calls.endpoint column
// value and as the Description field of the published PaymentRequirements.
//
// fallbackPrice is the hardcoded price the route was registered with — it
// is used only if the database lookup against x402_pricing fails (e.g. on
// first boot before the seeder has run). The authoritative price is the
// one stored in x402_pricing, so dashboard edits take effect immediately
// on the next request without needing a restart.
func (s *Server) X402Middleware(endpoint string, fallbackPrice float64) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		now := time.Now()

		// Resolve the live price from the operator's x402_pricing table.
		// On lookup failure (no row, disabled row, db error) we fall back
		// to the hardcoded route default — never fail open at zero.
		priceUSDC := fallbackPrice
		if row, err := s.store.GetX402PricingByOrgEndpoint(ctx, demoOrgID, endpoint); err == nil && row.Enabled {
			priceUSDC = row.PriceUSDC
		}

		// 1. Build the canonical PaymentRequirements for this endpoint.
		// Notice the v2 shape: amount (not maxAmountRequired), asset is the
		// SAC contract address (not the classic G-issuer), no resource/
		// description fields here — those live on the parent PaymentRequired.
		//
		// areFeesSponsored=true is REQUIRED by @x402/stellar's "exact" scheme:
		// the operator declares explicitly that the facilitator will absorb
		// the Stellar tx fee (XLM gas) for the Soroban auth entry. The
		// OpenZeppelin facilitator is built to do exactly this.
		requirement := x402.PaymentRequirements{
			Scheme:            x402SchemeExact,
			Network:           s.config.X402Network,
			Asset:             s.config.X402USDCContract,
			Amount:            formatAmount(priceUSDC),
			PayTo:             s.config.X402TreasuryAddress,
			MaxTimeoutSeconds: defaultMaxTimeoutSec,
			Extra: map[string]interface{}{
				"code":             s.config.X402USDCCode,
				"issuer":           s.config.X402USDCIssuer,
				"areFeesSponsored": true,
			},
		}
		paymentRequired := x402.PaymentRequired{
			X402Version: x402Version,
			Resource: x402.ResourceInfo{
				URL:         absoluteURL(ctx),
				Description: fmt.Sprintf("StellarFlow: %s", endpoint),
				MimeType:    "application/json",
			},
			Accepts: []x402.PaymentRequirements{requirement},
		}

		// 2. If no X-PAYMENT header, return 402 with the requirements.
		paymentHeader := ctx.GetHeader(headerXPayment)
		if paymentHeader == "" {
			paymentRequired.Error = "X-PAYMENT header missing"
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// 3. Decode + parse the payload.
		payload, err := decodePaymentPayload(paymentHeader)
		if err != nil {
			paymentRequired.Error = fmt.Sprintf("invalid X-PAYMENT header: %v", err)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// 4. Sanity-check the requirements the client says they accepted match ours.
		if payload.Accepted.Scheme != requirement.Scheme {
			paymentRequired.Error = fmt.Sprintf("scheme mismatch: payload=%q server=%q", payload.Accepted.Scheme, requirement.Scheme)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}
		if payload.Accepted.Network != requirement.Network {
			paymentRequired.Error = fmt.Sprintf("network mismatch: payload=%q server=%q", payload.Accepted.Network, requirement.Network)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}
		if payload.Accepted.PayTo != requirement.PayTo {
			paymentRequired.Error = fmt.Sprintf("payTo mismatch: payload=%q server=%q", payload.Accepted.PayTo, requirement.PayTo)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}
		if payload.Accepted.Amount != requirement.Amount {
			paymentRequired.Error = fmt.Sprintf("amount mismatch: payload=%q server=%q", payload.Accepted.Amount, requirement.Amount)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// 5. Verify with the facilitator (or skip in dry-run).
		var facilitatorName string
		var verifiedPayer string
		if s.config.X402DryRun {
			facilitatorName = "dry-run"
			verifiedPayer = extractDryRunPayer(payload.Payload)
		} else {
			if s.facilitator == nil {
				paymentRequired.Error = "facilitator not configured (set X402_FACILITATOR_API_KEY)"
				ctx.JSON(http.StatusPaymentRequired, paymentRequired)
				ctx.Abort()
				return
			}
			verifyCtx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
			defer cancel()
			verifyResp, err := s.facilitator.Verify(verifyCtx, x402.VerifyRequest{
				X402Version:         x402Version,
				PaymentPayload:      *payload,
				PaymentRequirements: requirement,
			})
			if err != nil {
				paymentRequired.Error = fmt.Sprintf("facilitator verify error: %v", err)
				ctx.JSON(http.StatusPaymentRequired, paymentRequired)
				ctx.Abort()
				return
			}
			if !verifyResp.IsValid {
				paymentRequired.Error = fmt.Sprintf("facilitator rejected payment: %s — %s", verifyResp.InvalidReason, verifyResp.InvalidMessage)
				ctx.JSON(http.StatusPaymentRequired, paymentRequired)
				ctx.Abort()
				return
			}
			facilitatorName = "openzeppelin"
			verifiedPayer = verifyResp.Payer
		}

		// 6. Stash everything on the context for the inner handler.
		// The reasoning header is URL-encoded by clients (HTTP headers are
		// limited to ASCII; LLM reasoning often has Unicode like em dashes
		// and smart quotes). Decode it back to its original UTF-8 form.
		rawReasoning := ctx.GetHeader(reasoningHeader)
		decodedReasoning := rawReasoning
		if rawReasoning != "" {
			if dec, err := url.QueryUnescape(rawReasoning); err == nil {
				decodedReasoning = dec
			}
		}
		x := &x402Context{
			Endpoint:     endpoint,
			PayerAddress: normalizeStellarAddress(verifiedPayer),
			PriceUSDC:    priceUSDC,
			Facilitator:  facilitatorName,
			ClientID:     ctx.GetHeader(clientIDHeader),
			Reasoning:    decodedReasoning,
			StartedAt:    now,
		}
		ctx.Set(x402ContextKey, x)

		// 7. Capture the response so we know the payload size.
		bodyBuf := &bytes.Buffer{}
		writer := &capturingWriter{ResponseWriter: ctx.Writer, body: bodyBuf}
		ctx.Writer = writer

		// 8. Call the inner handler.
		ctx.Next()

		// 9. Snapshot everything we need before kicking off the goroutine —
		// gin.Context is canceled as soon as the response finishes, so we
		// can't pass it (or anything from it) to the async DB insert or
		// settle call.
		callStatus := "paid"
		var errMsg sql.NullString
		if writer.statusCode >= 400 {
			callStatus = "failed"
			errMsg = sql.NullString{String: fmt.Sprintf("upstream returned %d", writer.statusCode), Valid: true}
		}
		responseBytes := int32(bodyBuf.Len())
		duration := int32(time.Since(x.StartedAt).Milliseconds())
		inputJSON := rawJSONOrEmpty(ctx.GetString("x402_input_json"))
		snapshot := *x // shallow copy of the x402Context
		settlePayload := *payload
		settleReq := requirement
		callIsLive := !s.config.X402DryRun

		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[x402] PANIC in agent_calls logger: %v\n", r)
				}
			}()
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// 9a. In live mode, call /settle to actually move the funds. We do
			// this AFTER the inner handler has already returned the response
			// (deferred settlement), so the user-visible latency is bounded
			// by verify, not verify+settle.
			var txHash sql.NullString
			if callIsLive && callStatus == "paid" && s.facilitator != nil {
				settleResp, err := s.facilitator.Settle(bgCtx, x402.VerifyRequest{
					X402Version:         x402Version,
					PaymentPayload:      settlePayload,
					PaymentRequirements: settleReq,
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "[x402] settle error for endpoint=%s payer=%s: %v\n", snapshot.Endpoint, snapshot.PayerAddress, err)
					callStatus = "failed"
					errMsg = sql.NullString{String: fmt.Sprintf("settle error: %v", err), Valid: true}
				} else if !settleResp.Success {
					fmt.Fprintf(os.Stderr, "[x402] settle failed for endpoint=%s payer=%s: %s — %s\n", snapshot.Endpoint, snapshot.PayerAddress, settleResp.ErrorReason, settleResp.ErrorMessage)
					callStatus = "failed"
					errMsg = sql.NullString{String: fmt.Sprintf("settle failed: %s", settleResp.ErrorReason), Valid: true}
				} else {
					txHash = sql.NullString{String: settleResp.Transaction, Valid: settleResp.Transaction != ""}
					if settleResp.Payer != "" {
						snapshot.PayerAddress = normalizeStellarAddress(settleResp.Payer)
					}
				}
			}

			// 9b. Insert the agent_calls row.
			row, err := s.store.CreateAgentCall(bgCtx, db.CreateAgentCallParams{
				OrgID:        demoOrgID,
				PayerAddress: snapshot.PayerAddress,
				Endpoint:     snapshot.Endpoint,
				RequestInput: inputJSON,
				ResponseSize: sql.NullInt32{Int32: responseBytes, Valid: true},
				PriceUSDC:    snapshot.PriceUSDC,
				TxHash:       txHash,
				Facilitator:  snapshot.Facilitator,
				Reasoning:    nullString(snapshot.Reasoning),
				ClientID:     nullString(snapshot.ClientID),
				Status:       callStatus,
				ErrorMessage: errMsg,
				DurationMs:   sql.NullInt32{Int32: duration, Valid: true},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[x402] CreateAgentCall failed for endpoint=%s payer=%s: %v\n", snapshot.Endpoint, snapshot.PayerAddress, err)
				return
			}

			// 9c. Publish to the live feed hub.
			if s.feedHub != nil {
				s.feedHub.Publish(FeedEvent{
					ID:           row.ID,
					OrgID:        row.OrgID,
					PayerAddress: row.PayerAddress,
					Endpoint:     row.Endpoint,
					PriceUSDC:    row.PriceUSDC,
					Facilitator:  row.Facilitator,
					Status:       row.Status,
					ClientID:     row.ClientID.String,
					Reasoning:    row.Reasoning.String,
					TxHash:       row.TxHash.String,
					DurationMs:   int(row.DurationMs.Int32),
					ResponseSize: int(row.ResponseSize.Int32),
					CreatedAt:    row.CreatedAt,
				})
			}
		}()
	}
}

// ----- Helpers -----

// decodePaymentPayload base64-decodes the X-PAYMENT header and unmarshals
// the v2 PaymentPayload envelope. Tries standard then URL-safe base64.
func decodePaymentPayload(header string) (*x402.PaymentPayload, error) {
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(header)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
	}
	var pl x402.PaymentPayload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, fmt.Errorf("json parse: %w", err)
	}
	if pl.X402Version == 0 {
		pl.X402Version = x402Version
	}
	return &pl, nil
}

// extractDryRunPayer pulls a "from" field out of the network-specific
// payload, if present. Used only in dry-run mode where the client doesn't
// sign anything and we accept whatever they say their address is.
func extractDryRunPayer(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var p struct {
		From string `json:"from"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	return p.From
}

// normalizeStellarAddress ensures the value fits the agent_calls.payer_address
// VARCHAR(56) constraint. Returns a deterministic placeholder for any string
// that isn't a 56-char G-prefixed address.
func normalizeStellarAddress(addr string) string {
	if len(addr) == 56 && addr[0] == 'G' {
		return addr
	}
	return "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADRYRUNANONYMOUSXXXXX"
}

// formatAmount renders a USDC amount as an INTEGER STRING in the asset's
// atomic unit (stroops for Stellar USDC, 7 decimals = 10,000,000 stroops
// per USDC). The x402 v2 spec requires PaymentRequirements.amount to be
// an integer string in the smallest unit of the asset, NOT a decimal —
// the @x402/stellar SDK rejects decimals at signing time.
//
// Example: $0.05 USDC → "500000" stroops, $0.20 USDC → "2000000".
func formatAmount(usdc float64) string {
	const usdcStroopsPerUnit = 10_000_000 // 10^7
	stroops := int64(math.Round(usdc * float64(usdcStroopsPerUnit)))
	return fmt.Sprintf("%d", stroops)
}

// absoluteURL reconstructs the request's URL as it would appear to a client,
// for use in PaymentRequired.Resource.URL.
func absoluteURL(ctx *gin.Context) string {
	scheme := "http"
	if ctx.Request.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, ctx.Request.Host, ctx.Request.URL.Path)
}

// capturingWriter wraps gin's ResponseWriter to record the body and status.
type capturingWriter struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (w *capturingWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *capturingWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func rawJSONOrEmpty(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(s)
}

// GetX402Context retrieves the x402 metadata stashed by the middleware.
// Handlers wrapped with X402Middleware can rely on this returning non-nil.
func GetX402Context(ctx *gin.Context) *x402Context {
	v, ok := ctx.Get(x402ContextKey)
	if !ok {
		return nil
	}
	return v.(*x402Context)
}
