package sidecar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/your-org/stellarflow/internal/x402"

	"github.com/gin-gonic/gin"
)

const (
	headerXPayment       = "X-Payment"
	x402SchemeExact      = "exact"
	x402Version          = 2
	defaultMaxTimeoutSec = 30
)

// X402Config holds the Stellar network + asset config for building 402 challenges.
type X402Config struct {
	Network         string // "stellar:testnet"
	USDCContract    string // Soroban SAC C-address
	USDCIssuer      string // classic G-issuer (informational)
	USDCCode        string // "USDC"
	TreasuryAddress string // G-address that receives payments
	DryRun          bool
}

// X402Middleware returns a gin middleware that gates a request behind an x402
// payment of the given USDC amount. It uses the facilitator to verify + settle,
// and calls the provided onPaid callback with metadata for logging/SSE.
func X402Middleware(
	cfg X402Config,
	facilitator *x402.Client,
	endpoint string,
	priceUSDC float64,
	onPaid func(call PaidCall),
) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		now := time.Now()

		requirement := x402.PaymentRequirements{
			Scheme:            x402SchemeExact,
			Network:           cfg.Network,
			Asset:             cfg.USDCContract,
			Amount:            formatAmount(priceUSDC),
			PayTo:             cfg.TreasuryAddress,
			MaxTimeoutSeconds: defaultMaxTimeoutSec,
			Extra: map[string]interface{}{
				"code":             cfg.USDCCode,
				"issuer":           cfg.USDCIssuer,
				"areFeesSponsored": true,
			},
		}
		paymentRequired := x402.PaymentRequired{
			X402Version: x402Version,
			Resource: x402.ResourceInfo{
				URL:         absoluteURL(ctx),
				Description: endpoint,
				MimeType:    "application/json",
			},
			Accepts: []x402.PaymentRequirements{requirement},
		}

		// No X-PAYMENT header → 402.
		paymentHeader := ctx.GetHeader(headerXPayment)
		if paymentHeader == "" {
			paymentRequired.Error = "X-PAYMENT header missing"
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// Decode + parse the payload.
		payload, err := decodePaymentPayload(paymentHeader)
		if err != nil {
			paymentRequired.Error = fmt.Sprintf("invalid X-PAYMENT header: %v", err)
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// Validate the payload matches our requirements.
		if payload.Accepted.Scheme != requirement.Scheme ||
			payload.Accepted.Network != requirement.Network ||
			payload.Accepted.PayTo != requirement.PayTo ||
			payload.Accepted.Amount != requirement.Amount {
			paymentRequired.Error = "payment requirements mismatch"
			ctx.JSON(http.StatusPaymentRequired, paymentRequired)
			ctx.Abort()
			return
		}

		// Verify + settle.
		var facilitatorName string
		var verifiedPayer string
		if cfg.DryRun {
			facilitatorName = "dry-run"
			verifiedPayer = extractDryRunPayer(payload.Payload)
		} else {
			if facilitator == nil {
				paymentRequired.Error = "facilitator not configured"
				ctx.JSON(http.StatusPaymentRequired, paymentRequired)
				ctx.Abort()
				return
			}
			verifyCtx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
			defer cancel()
			verifyResp, err := facilitator.Verify(verifyCtx, x402.VerifyRequest{
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
				paymentRequired.Error = fmt.Sprintf("payment rejected: %s — %s", verifyResp.InvalidReason, verifyResp.InvalidMessage)
				ctx.JSON(http.StatusPaymentRequired, paymentRequired)
				ctx.Abort()
				return
			}
			facilitatorName = "openzeppelin"
			verifiedPayer = verifyResp.Payer
		}

		// Stash payer on context for the proxy handler.
		ctx.Set("payer_address", normalizeStellarAddress(verifiedPayer))

		// Run the proxy (next handler).
		ctx.Next()

		// Post-handler: settle + log.
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[x402-sidecar] PANIC in settle/log: %v\n", r)
				}
			}()

			var txHash string
			if !cfg.DryRun && facilitator != nil {
				bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				settleResp, err := facilitator.Settle(bgCtx, x402.VerifyRequest{
					X402Version:         x402Version,
					PaymentPayload:      *payload,
					PaymentRequirements: requirement,
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "[x402-sidecar] settle error endpoint=%s payer=%s: %v\n", endpoint, verifiedPayer, err)
				} else if !settleResp.Success {
					fmt.Fprintf(os.Stderr, "[x402-sidecar] settle failed endpoint=%s: %s — %s\n", endpoint, settleResp.ErrorReason, settleResp.ErrorMessage)
				} else {
					txHash = settleResp.Transaction
					if settleResp.Payer != "" {
						verifiedPayer = settleResp.Payer
					}
				}
			}

			call := PaidCall{
				Endpoint:     endpoint,
				PayerAddress: normalizeStellarAddress(verifiedPayer),
				PriceUSDC:    priceUSDC,
				Facilitator:  facilitatorName,
				TxHash:       txHash,
				DurationMs:   int(time.Since(now).Milliseconds()),
				Timestamp:    now,
			}
			if onPaid != nil {
				onPaid(call)
			}
		}()
	}
}

// PaidCall is the metadata emitted after a successful paid call.
type PaidCall struct {
	Endpoint     string    `json:"endpoint"`
	PayerAddress string    `json:"payer_address"`
	PriceUSDC    float64   `json:"price_usdc"`
	Facilitator  string    `json:"facilitator"`
	TxHash       string    `json:"tx_hash,omitempty"`
	DurationMs   int       `json:"duration_ms"`
	Timestamp    time.Time `json:"timestamp"`
}

// --- helpers (mirrored from internal/api/middleware_x402.go) ---

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

func normalizeStellarAddress(addr string) string {
	if len(addr) == 56 && addr[0] == 'G' {
		return addr
	}
	return "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADRYRUNANONYMOUSXXXXX"
}

func formatAmount(usdc float64) string {
	stroops := int64(math.Round(usdc * 10_000_000))
	return fmt.Sprintf("%d", stroops)
}

func absoluteURL(ctx *gin.Context) string {
	scheme := "http"
	if ctx.Request.TLS != nil || ctx.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := ctx.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = ctx.Request.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, ctx.Request.URL.Path)
}

// rawReasoningHeader extracts and URL-decodes the reasoning header.
func rawReasoningHeader(ctx *gin.Context) string {
	raw := ctx.GetHeader("X-StellarFlow-Reason")
	if raw == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(raw); err == nil {
		return dec
	}
	return raw
}
