package api

import (
	"fmt"
	"strings"

	"github.com/your-org/stellarflow/internal/config"
	db "github.com/your-org/stellarflow/internal/db/sqlc"
	"github.com/your-org/stellarflow/internal/stellar"
	"github.com/your-org/stellarflow/internal/token"
	"github.com/your-org/stellarflow/internal/x402"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// X402EndpointRegistry is the interface that example packages implement
// to register their paid endpoints with the wire's gin router. The wire
// (this package) only depends on this interface, never on a concrete
// example package — keeping the wire/example boundary clean.
//
// See backend/example/stellarflow/handlers/handlers.go for the bundled
// reference implementation. To use a different example with this
// template, write your own type that implements this method and pass
// it to NewServer.
type X402EndpointRegistry interface {
	RegisterRoutes(group *gin.RouterGroup, middleware func(endpoint string, fallbackPriceUSDC float64) gin.HandlerFunc)
}

// Server holds all dependencies for the HTTP API
type Server struct {
	config        config.Config
	store         *db.Store
	tokenMaker    token.Maker
	sep10         *stellar.SEP10
	sponsor       *stellar.Sponsor
	facilitator   *x402.Client
	feedHub       *FeedHub
	router        *gin.Engine
	x402Endpoints X402EndpointRegistry // optional — registers paid endpoints
}

// NewServer creates a new HTTP server with all routes configured.
//
// x402Endpoints is the example package that owns the paid endpoint
// implementations. Pass nil to run the wire alone (no /api/x402/* routes
// will be registered, useful for testing the wire in isolation). For the
// bundled StellarFlow example, pass `stellarflowhandlers.New()` from
// cmd/api/main.go.
func NewServer(
	cfg config.Config,
	store *db.Store,
	x402Endpoints X402EndpointRegistry,
) (*Server, error) {
	tokenMaker, err := token.NewPasetoMaker(cfg.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	// SEP-10 wallet auth helper. Optional — if no server keypair is configured,
	// the /api/auth/challenge and /api/auth/token endpoints return 503 and the
	// rest of the server keeps working.
	var sep10Helper *stellar.SEP10
	if cfg.Sep10ServerSecretKey != "" {
		sep10Helper, err = stellar.New(
			cfg.Sep10ServerSecretKey,
			cfg.Sep10HomeDomain,
			cfg.Sep10WebAuthDomain,
			cfg.X402Network,
		)
		if err != nil {
			return nil, fmt.Errorf("cannot init SEP-10: %w", err)
		}
		logrus.WithField("home_domain", cfg.Sep10HomeDomain).Info("SEP-10 wallet auth enabled")
	} else {
		logrus.Warn("SEP10_SERVER_SECRET_KEY not set — wallet sign-in disabled")
	}

	// x402 facilitator client. Optional — if no API key is configured, the
	// server still boots and the dry-run code path keeps working. Live
	// verification just returns a 402 explaining that no facilitator is
	// configured.
	var facilitatorClient *x402.Client
	if cfg.X402FacilitatorAPIKey != "" && cfg.X402FacilitatorURL != "" {
		facilitatorClient = x402.New(cfg.X402FacilitatorURL, cfg.X402FacilitatorAPIKey)
		logrus.WithFields(logrus.Fields{
			"facilitator": cfg.X402FacilitatorURL,
			"network":     cfg.X402Network,
			"dry_run":     cfg.X402DryRun,
		}).Info("x402 facilitator client configured")
	} else {
		logrus.Warn("X402_FACILITATOR_API_KEY not set — live verification disabled, dry-run only")
	}

	// Stellar sponsor — funds new agent accounts via /api/agent/sponsor.
	// Optional: if no treasury secret is configured the endpoint returns
	// 503 sponsor_disabled and the rest of the server keeps working.
	var sponsorHelper *stellar.Sponsor
	if cfg.X402TreasurySecret != "" {
		passphrase := networkPassphraseFor(cfg.X402Network)
		sponsorHelper, err = stellar.NewSponsor(cfg.X402TreasurySecret, passphrase)
		if err != nil {
			return nil, fmt.Errorf("cannot init Stellar sponsor: %w", err)
		}
		// Sanity check: treasury secret must match the configured public address.
		if cfg.X402TreasuryAddress != "" && sponsorHelper.TreasuryAddress() != cfg.X402TreasuryAddress {
			return nil, fmt.Errorf(
				"X402_TREASURY_SECRET (%s) does not match X402_TREASURY_ADDRESS (%s)",
				sponsorHelper.TreasuryAddress(), cfg.X402TreasuryAddress,
			)
		}
		logrus.WithField("treasury", sponsorHelper.TreasuryAddress()).Info("Stellar sponsor enabled")
	} else {
		logrus.Warn("X402_TREASURY_SECRET not set — agent sponsoring disabled")
	}

	server := &Server{
		config:        cfg,
		store:         store,
		tokenMaker:    tokenMaker,
		sep10:         sep10Helper,
		sponsor:       sponsorHelper,
		facilitator:   facilitatorClient,
		feedHub:       NewFeedHub(),
		x402Endpoints: x402Endpoints,
	}

	server.setupRouter()
	return server, nil
}

// Router exposes the gin.Engine so the composition root (cmd/api/main.go)
// can register additional routes after the wire is set up — for example,
// a public demo endpoint that bypasses the paywall for the landing page.
func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) setupRouter() {
	if s.config.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	// Sentry middleware — captures panics and adds request context to errors.
	// Repanic=true so gin.Recovery still handles the HTTP response.
	router.Use(sentrygin.New(sentrygin.Options{
		Repanic:         true,
		WaitForDelivery: false,
	}))
	router.Use(ErrorLoggingMiddleware())

	// CORS
	allowedOrigins := strings.Split(s.config.AllowedOrigins, ",")
	for i := range allowedOrigins {
		allowedOrigins[i] = strings.TrimSpace(allowedOrigins[i])
	}
	router.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	setupRoutes(router, s)
	s.router = router
}

// Start runs the HTTP server
func (s *Server) Start(addr string) error {
	return s.router.Run(addr)
}

// ErrorResponse returns a standardized error JSON body. Exported so the
// example handlers (under example/stellarflow/handlers) can reuse the same
// shape without redefining it.
func ErrorResponse(message string, code string) gin.H {
	return gin.H{
		"error": message,
		"code":  code,
	}
}

// errorResponse is the internal alias kept for backward compatibility with
// the wire's own handlers. New code should call ErrorResponse directly.
func errorResponse(message string, code string) gin.H {
	return ErrorResponse(message, code)
}
