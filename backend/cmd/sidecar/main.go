// x402-sidecar — drop-in x402 payment proxy for any HTTP backend.
//
// Usage:
//
//	X402_ENDPOINTS_FILE=endpoints.yaml \
//	X402_PROXY_TARGET=http://localhost:4000 \
//	X402_TREASURY_ADDRESS=GXXX... \
//	X402_TREASURY_SECRET=SXXX... \
//	X402_FACILITATOR_API_KEY=abc123 \
//	./x402-sidecar
//
// The sidecar sits between agents and your real backend. It handles the
// x402 paywall (402 challenge, payment verification, on-chain settlement)
// and proxies paid requests to your backend. Your backend code never
// changes — it receives normal HTTP requests with an extra header
// X-Payer-Address containing the Stellar G-address that paid.
//
// No database, no Redis, no worker. Just a YAML config + the OZ facilitator.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/your-org/stellarflow/internal/sidecar"
	"github.com/your-org/stellarflow/internal/stellar"
	"github.com/your-org/stellarflow/internal/x402"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	stellarnetwork "github.com/stellar/go/network"
)

func main() {
	// --- Config from env ---
	endpointsFile := envOrDefault("X402_ENDPOINTS_FILE", "endpoints.yaml")
	proxyTarget := envOrDefault("X402_PROXY_TARGET", "http://localhost:4000")
	listenAddr := envOrDefault("X402_LISTEN_ADDR", "0.0.0.0:8086")
	allowedOrigins := envOrDefault("X402_ALLOWED_ORIGINS", "*")
	dryRun := strings.ToLower(envOrDefault("X402_DRY_RUN", "true")) == "true"

	network := envOrDefault("X402_NETWORK", "stellar:testnet")
	treasuryAddr := os.Getenv("X402_TREASURY_ADDRESS")
	treasurySecret := os.Getenv("X402_TREASURY_SECRET")
	facilitatorURL := envOrDefault("X402_FACILITATOR_URL", "https://channels.openzeppelin.com/x402/testnet")
	facilitatorKey := os.Getenv("X402_FACILITATOR_API_KEY")

	usdcIssuer := envOrDefault("X402_USDC_ISSUER", "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5")
	usdcContract := envOrDefault("X402_USDC_CONTRACT", "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA")
	usdcCode := envOrDefault("X402_USDC_CODE", "USDC")

	// --- Logger ---
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.SetOutput(os.Stdout)

	// --- Load endpoints YAML ---
	epCfg, err := sidecar.LoadEndpoints(endpointsFile)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load endpoints config")
	}
	logrus.WithField("endpoints", len(epCfg.Endpoints)).Info("loaded endpoints from YAML")

	// --- Facilitator client (optional in dry-run) ---
	var facilitator *x402.Client
	if facilitatorKey != "" && facilitatorURL != "" {
		facilitator = x402.New(facilitatorURL, facilitatorKey)
		logrus.WithFields(logrus.Fields{
			"url":     facilitatorURL,
			"dry_run": dryRun,
		}).Info("facilitator client configured")
	} else if !dryRun {
		logrus.Warn("X402_FACILITATOR_API_KEY not set — live mode requires a facilitator; falling back to dry-run")
		dryRun = true
	}

	// --- Sponsor (optional) ---
	var sponsorHelper *stellar.Sponsor
	if treasurySecret != "" {
		// NewSponsor expects the Stellar network passphrase, not our env-style string.
		passphrase := stellarnetwork.TestNetworkPassphrase
		sponsorHelper, err = stellar.NewSponsor(treasurySecret, passphrase)
		if err != nil {
			logrus.WithError(err).Fatal("failed to create sponsor from treasury secret")
		}
		logrus.WithField("treasury", sponsorHelper.TreasuryAddress()).Info("treasury sponsor enabled")
	} else {
		logrus.Info("X402_TREASURY_SECRET not set — /api/agent/sponsor disabled")
	}

	x402Cfg := sidecar.X402Config{
		Network:         network,
		USDCContract:    usdcContract,
		USDCIssuer:      usdcIssuer,
		USDCCode:        usdcCode,
		TreasuryAddress: treasuryAddr,
		DryRun:          dryRun,
	}

	// --- Paid call logger (structured JSON to stdout) ---
	onPaid := func(call sidecar.PaidCall) {
		data, _ := json.Marshal(call)
		logrus.WithField("event", "paid_call").Info(string(data))
	}

	// --- Gin router ---
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// CORS
	corsConfig := cors.DefaultConfig()
	if allowedOrigins == "*" {
		corsConfig.AllowAllOrigins = true
	} else {
		corsConfig.AllowOrigins = strings.Split(allowedOrigins, ",")
	}
	corsConfig.AllowHeaders = append(corsConfig.AllowHeaders, "X-Payment", "X-Payer-Address", "Authorization")
	router.Use(cors.New(corsConfig))

	// --- Public catalog ---
	router.GET("/api/catalog", sidecar.CatalogHandler(epCfg.Endpoints, x402Cfg))

	// --- Optional: agent sponsor ---
	if sponsorHelper != nil {
		router.POST("/api/agent/sponsor", sponsorHandler(sponsorHelper))
	}

	// --- Register x402-gated proxy routes ---
	for _, ep := range epCfg.Endpoints {
		proxyHandler, err := sidecar.ReverseProxyHandler(proxyTarget)
		if err != nil {
			logrus.WithError(err).WithField("target", proxyTarget).Fatal("failed to create proxy handler")
		}

		mw := sidecar.X402Middleware(x402Cfg, facilitator, ep.Path, ep.PriceUSDC, onPaid)

		method := strings.ToUpper(ep.Method)
		switch method {
		case "GET":
			router.GET(ep.Path, mw, proxyHandler)
		case "POST":
			router.POST(ep.Path, mw, proxyHandler)
		case "PUT":
			router.PUT(ep.Path, mw, proxyHandler)
		case "DELETE":
			router.DELETE(ep.Path, mw, proxyHandler)
		case "PATCH":
			router.PATCH(ep.Path, mw, proxyHandler)
		default:
			router.POST(ep.Path, mw, proxyHandler)
		}

		logrus.WithFields(logrus.Fields{
			"method": method,
			"path":   ep.Path,
			"price":  fmt.Sprintf("$%.2f", ep.PriceUSDC),
		}).Info("registered x402 endpoint → proxy")
	}

	// --- Health check ---
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":    "ok",
			"mode":      "sidecar",
			"dry_run":   dryRun,
			"endpoints": len(epCfg.Endpoints),
			"target":    proxyTarget,
		})
	})

	// --- Start ---
	logrus.WithFields(logrus.Fields{
		"addr":      listenAddr,
		"target":    proxyTarget,
		"endpoints": len(epCfg.Endpoints),
		"dry_run":   dryRun,
		"network":   network,
	}).Info("x402-sidecar starting")

	if err := router.Run(listenAddr); err != nil {
		logrus.WithError(err).Fatal("server failed")
	}
}

// sponsorHandler wraps the stellar.Sponsor into a gin handler.
func sponsorHandler(sponsor *stellar.Sponsor) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req struct {
			Address string `json:"address" binding:"required"`
		}
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if !stellar.IsValidStellarAddress(req.Address) {
			ctx.JSON(400, gin.H{"error": "invalid Stellar address"})
			return
		}
		// SponsorAgent returns ("", nil) if the account already exists (idempotent).
		txHash, err := sponsor.SponsorAgent(ctx.Request.Context(), req.Address)
		if err != nil {
			logrus.WithError(err).WithField("address", req.Address).Error("sponsor failed")
			ctx.JSON(503, gin.H{"error": err.Error()})
			return
		}
		alreadyExisted := txHash == ""
		ctx.JSON(200, gin.H{
			"tx_hash":         txHash,
			"already_existed": alreadyExisted,
		})
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
