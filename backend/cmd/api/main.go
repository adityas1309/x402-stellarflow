// stellarflow backend composition root.
//
// Everything this binary does:
//   1. Load env config (backend/.env)
//   2. Connect to Postgres (persistence for calls, pricing, sessions)
//   3. Instantiate the StellarFlow example (the replaceable part)
//   4. Start the HTTP server with the x402 + SEP-10 wire
//
// The example (StellarFlow / sentiment analysis) is constructed here and
// injected into NewServer via the X402EndpointRegistry interface. The wire
// in internal/api/ never imports the example — replace example/stellarflow/
// with your own package when forking the template and update this file.
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	stellarflowhandlers "github.com/your-org/stellarflow/example/stellarflow/handlers"
	"github.com/your-org/stellarflow/internal/api"
	"github.com/your-org/stellarflow/internal/config"
	db "github.com/your-org/stellarflow/internal/db/sqlc"
	"github.com/your-org/stellarflow/internal/stellar"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig(".")
	if err != nil {
		log.Fatalf("cannot load config: %v", err)
	}

	// Configure logger
	logrus.SetFormatter(&logrus.JSONFormatter{})
	if cfg.Env == "development" {
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}
	logrus.SetOutput(os.Stdout)

	// Sentry — initialize early so panics during startup are captured
	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      cfg.Env,
			TracesSampleRate: 0.1,
			AttachStacktrace: true,
		})
		if err != nil {
			logrus.WithError(err).Warn("Sentry initialization failed")
		} else {
			logrus.Info("Sentry initialized")
			defer sentry.Flush(2 * time.Second)
			logrus.AddHook(newSentryLogrusHook())
		}
	}

	// Connect to PostgreSQL
	sqlDB, err := sql.Open("postgres", cfg.DBSource)
	if err != nil {
		logrus.WithError(err).Fatal("cannot connect to database")
	}
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		logrus.WithError(err).Fatal("cannot ping database")
	}
	logrus.Info("connected to database")

	store := db.NewStore(sqlDB)

	// Example: StellarFlow — a sentiment analysis service built on top of the
	// wire. Single paid endpoint (/api/x402/sentiment) that calls Apify for
	// real-time social media scraping and OpenAI for sentiment analysis.
	// Falls back to deterministic mock if APIFY_TOKEN or OPENAI_API_KEY are
	// not set. Replace this package when forking the template for your SaaS.
	exampleEndpoints := stellarflowhandlers.New()

	server, err := api.NewServer(cfg, store, exampleEndpoints)
	if err != nil {
		logrus.WithError(err).Fatal("cannot create server")
	}

	// Public demo endpoint — calls the same Apify+OpenAI handler WITHOUT
	// the x402 paywall. Used by the landing page at stellarflow.example.com
	// so judges can see a REAL response without needing to sign a payment.
	// The cost is absorbed by the operator (~$0.02/call in Apify+OpenAI).
	server.Router().POST("/api/demo/sentiment", exampleEndpoints.X402Sentiment)

	// Paid demo endpoint — agent pays REAL USDC before getting the response.
	// Used by the landing page to show actual money movement on Stellar.
	// Requires DEMO_AGENT_SECRET in .env (the agent wallet's S-key).
	if agentSecret := os.Getenv("DEMO_AGENT_SECRET"); agentSecret != "" {
		passphrase := "Test SDF Network ; September 2015"
		agentPayer, err := stellar.NewSponsor(agentSecret, passphrase)
		if err != nil {
			logrus.WithError(err).Warn("DEMO_AGENT_SECRET invalid — paid demo disabled")
		} else {
			treasuryAddr := cfg.X402TreasuryAddress
			usdcIssuer := cfg.X402USDCIssuer
			logrus.WithFields(logrus.Fields{
				"agent":    agentPayer.TreasuryAddress(),
				"treasury": treasuryAddr,
			}).Info("paid demo endpoint enabled (agent → treasury USDC)")

			server.Router().POST("/api/demo/paid-sentiment", func(c *gin.Context) {
				var req struct {
					Topic string `json:"topic" binding:"required"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}

			// Step 1: Agent pays 0.10 USDC to treasury on Stellar testnet
				txHash, err := agentPayer.TransferUSDC(
					c.Request.Context(), treasuryAddr, "0.1000000", usdcIssuer,
				)
				if err != nil {
					c.JSON(http.StatusPaymentRequired, gin.H{
						"error":         "payment_failed",
						"message":       err.Error(),
						"agent_address": agentPayer.TreasuryAddress(),
						"agent_balance": getUSDCBalance(agentPayer, usdcIssuer),
						"required":      "0.10 USDC",
					})
					return
				}

				// Step 2: Payment settled — run the real Apify+OpenAI analysis
				body, _ := json.Marshal(map[string]string{"topic": req.Topic})
				fakeReq := httptest.NewRequest("POST", "/api/demo/sentiment", bytes.NewReader(body))
				fakeReq.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				server.Router().ServeHTTP(w, fakeReq)

				var analysis json.RawMessage
				json.Unmarshal(w.Body.Bytes(), &analysis)

				c.JSON(http.StatusOK, gin.H{
					"payment": gin.H{
						"tx_hash":  txHash,
						"amount":   "0.10",
						"asset":    "USDC",
						"from":     agentPayer.TreasuryAddress(),
						"to":       treasuryAddr,
						"explorer": "https://stellarchain.io/transactions/" + txHash,
					},
					"analysis": analysis,
				})
			})
		}
	} else {
		logrus.Warn("DEMO_AGENT_SECRET not set — paid demo endpoint disabled, using free demo only")
	}

	logrus.WithField("address", cfg.ServerAddress).Info("starting HTTP server")
	if err := server.Start(cfg.ServerAddress); err != nil {
		logrus.WithError(err).Fatal("cannot start server")
	}
}

// getUSDCBalance reads the current USDC balance of a Sponsor's keypair from Horizon.
// Returns "unknown" on any error — used for diagnostic info in error responses.
func getUSDCBalance(s *stellar.Sponsor, usdcIssuer string) string {
	// Sponsor doesn't expose a USDC balance method, so we reuse TreasuryBalance
	// as a proxy. For the paid demo, "treasury" is actually the agent keypair.
	bal, err := s.TreasuryBalance(nil)
	if err != nil {
		return "unknown"
	}
	return bal + " XLM (check USDC on explorer)"
}
