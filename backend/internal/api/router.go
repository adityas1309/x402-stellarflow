package api

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed static/*
var staticFS embed.FS

func setupRoutes(router *gin.Engine, s *Server) {
	// Static demo page (Agent Desk) — served at /demo and /demo/agent-desk.html.
	// Embedded so the binary is self-contained.
	staticSub, _ := fs.Sub(staticFS, "static")
	router.StaticFS("/demo", http.FS(staticSub))
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/demo/agent-desk.html")
	})

	api := router.Group("/api")

	// Auth routes — SEP-10 wallet sign-in is the only path. Email/password
	// register/login/refresh have been removed for the hackathon: the wallet
	// IS the identity.
	auth := api.Group("/auth")
	{
		auth.GET("/challenge", s.sep10Challenge)
		auth.POST("/token", s.sep10Token)
	}

	// x402 paid public endpoints — NO auth (the wallet IS the identity).
	// Paid x402 endpoints — registered by whichever example package was
	// passed to NewServer. The wire knows nothing about specific endpoints
	// (their names, prices, request/response shapes); the example package
	// owns all of that and registers them via X402EndpointRegistry.
	//
	// For the bundled StellarFlow example, this calls
	// stellarflowhandlers.ExampleService.RegisterRoutes(x402, s.X402Middleware).
	x402 := api.Group("/x402")
	if s.x402Endpoints != nil {
		s.x402Endpoints.RegisterRoutes(x402, s.X402Middleware)
	}

	// Public catalog — discoverable by agents/developers, no auth.
	api.GET("/catalog", s.publicCatalog)

	// Agent wallet provisioning — funds a fresh G-address with 2 XLM so the
	// agent can establish its USDC trustline. Public, rate-limited per IP.
	api.POST("/agent/sponsor", s.sponsorAgent)

	// Operator live feed (SSE) — public because EventSource cannot send a
	// custom Authorization header. The data exposed is the same that the
	// /api/catalog already publishes (endpoint name, price, anonymized
	// payer address shorthand) so making it public doesn't leak anything
	// sensitive.
	publicOperator := api.Group("/operator")
	{
		publicOperator.GET("/feed", s.operatorFeed)
	}

	// Authenticated routes — all require a SEP-10 issued PASETO token in
	// the Authorization header.
	authed := api.Group("/")
	authed.Use(authMiddleware(s.tokenMaker))

	// Operator dashboard endpoints (rest of /api/operator/*). Require a
	// valid SEP-10 token. Still single-tenant scoped to demoOrgID for the
	// hackathon — production would additionally enforce that the caller's
	// wallet matches an OPERATOR_WALLETS allowlist.
	authedOperator := authed.Group("/operator")
	{
		authedOperator.GET("/recent", s.operatorRecent)
		authedOperator.GET("/revenue", s.operatorRevenue)
		authedOperator.GET("/top", s.operatorTop)
		authedOperator.GET("/pricing", s.listOperatorPricing)
		authedOperator.POST("/pricing", s.upsertOperatorPricing)
	}

	// Per-agent endpoints — read the wallet from the SEP-10 token payload
	// and return data scoped to that wallet only. Used by the public
	// /wallet/:address dashboard page and by anyone signing in with their
	// own burner wallet.
	authedAgent := authed.Group("/agent")
	{
		authedAgent.GET("/me/usage", s.agentUsage)
	}
}
