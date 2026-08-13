package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	db "github.com/your-org/stellarflow/internal/db/sqlc"
	"github.com/your-org/stellarflow/internal/stellar"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SEP-10 wallet sign-in
//
// Two endpoints:
//
//   GET  /api/auth/challenge?account=G... → returns an unsigned challenge tx
//                                            for the wallet to sign
//   POST /api/auth/token                  → verifies a signed challenge and
//                                            issues a Paseto access token
//
// On the first successful sign-in for a wallet, we synthesize an organization
// and a user record so the existing PASETO/role-based middleware just keeps
// working unchanged.

type sep10ChallengeResponse struct {
	ID                uuid.UUID `json:"id"`
	Transaction       string    `json:"transaction"`        // base64 XDR
	NetworkPassphrase string    `json:"network_passphrase"` // for client signing
	HomeDomain        string    `json:"home_domain"`
	ExpiresAt         time.Time `json:"expires_at"`
}

func (s *Server) sep10Challenge(ctx *gin.Context) {
	if s.sep10 == nil {
		ctx.JSON(http.StatusServiceUnavailable, errorResponse("SEP-10 not configured", "sep10_disabled"))
		return
	}

	account := strings.TrimSpace(ctx.Query("account"))
	if !stellar.IsValidStellarAddress(account) {
		ctx.JSON(http.StatusBadRequest, errorResponse("invalid 'account' query parameter (must be a Stellar G-address)", "invalid_account"))
		return
	}

	xdrStr, nonce, err := s.sep10.BuildChallenge(account)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(fmt.Sprintf("build challenge: %v", err), "internal_error"))
		return
	}

	expiresAt := time.Now().Add(s.sep10.ChallengeTimeout)
	challenge, err := s.store.CreateSep10Challenge(ctx, db.CreateSep10ChallengeParams{
		Address:        account,
		TransactionXDR: xdrStr,
		Nonce:          nonce,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to persist challenge", "internal_error"))
		return
	}

	ctx.JSON(http.StatusOK, sep10ChallengeResponse{
		ID:                challenge.ID,
		Transaction:       xdrStr,
		NetworkPassphrase: s.sep10.NetworkPassphrase,
		HomeDomain:        s.sep10.HomeDomain,
		ExpiresAt:         expiresAt,
	})
}

type sep10TokenRequest struct {
	ID          string `json:"id" binding:"required"`          // challenge UUID
	Transaction string `json:"transaction" binding:"required"` // signed XDR
}

func (s *Server) sep10Token(ctx *gin.Context) {
	if s.sep10 == nil {
		ctx.JSON(http.StatusServiceUnavailable, errorResponse("SEP-10 not configured", "sep10_disabled"))
		return
	}

	var req sep10TokenRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err.Error(), "validation_error"))
		return
	}

	id, err := uuid.Parse(req.ID)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse("invalid challenge id", "invalid_challenge"))
		return
	}

	challenge, err := s.store.GetSep10ChallengeByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.JSON(http.StatusUnauthorized, errorResponse("challenge not found", "unauthorized"))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load challenge", "internal_error"))
		return
	}

	if challenge.UsedAt.Valid {
		ctx.JSON(http.StatusUnauthorized, errorResponse("challenge already used", "unauthorized"))
		return
	}
	if time.Now().After(challenge.ExpiresAt) {
		ctx.JSON(http.StatusUnauthorized, errorResponse("challenge expired", "unauthorized"))
		return
	}

	if err := s.sep10.VerifyChallenge(challenge.TransactionXDR, req.Transaction, challenge.Address); err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(fmt.Sprintf("signature verification failed: %v", err), "unauthorized"))
		return
	}

	if err := s.store.MarkSep10ChallengeUsed(ctx, id); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to mark challenge used", "internal_error"))
		return
	}

	// Look up the wallet — if it doesn't exist yet, this is the operator's
	// first sign-in. We synthesize an org + user with placeholder credentials
	// that can never be used to log in via the email/password flow.
	wallet, err := s.store.GetWalletByAddress(ctx, challenge.Address)
	var user db.User
	var org db.Organization
	switch {
	case err == nil:
		// Returning operator — load their user + org.
		user, err = s.store.GetUserByID(ctx, wallet.UserID)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load user", "internal_error"))
			return
		}
		org, err = s.store.GetOrganizationByID(ctx, user.OrgID)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, errorResponse("failed to load organization", "internal_error"))
			return
		}
		_ = s.store.TouchWalletLogin(ctx, wallet.ID)
	case errors.Is(err, sql.ErrNoRows):
		// New operator — bootstrap org + user + wallet atomically (best-effort;
		// no transaction wrapper because the existing Store doesn't expose one
		// generically and the failure mode of partial state is acceptable for
		// a hackathon: the operator can retry).
		shortAddr := walletShorthand(challenge.Address)
		slug := "op-" + strings.ToLower(shortAddr) + "-" + time.Now().Format("060102150405")
		org, err = s.store.CreateOrganization(ctx, db.CreateOrganizationParams{
			Name:    "Operator " + shortAddr,
			Slug:    slug,
			LogoURL: sql.NullString{},
			Plan:    "starter",
			Credits: 0,
		})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, errorResponse("failed to create organization", "internal_error"))
			return
		}
		user, err = s.store.CreateUser(ctx, db.CreateUserParams{
			OrgID:          org.ID,
			Email:          "wallet+" + challenge.Address + "@stellar.local",
			HashedPassword: "WALLET_AUTH_NO_PASSWORD",
			FullName:       "Operator " + shortAddr,
			Role:           "owner",
		})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, errorResponse(fmt.Sprintf("failed to create user: %v", err), "internal_error"))
			return
		}
		_, err = s.store.CreateWallet(ctx, db.CreateWalletParams{
			UserID:  user.ID,
			Address: challenge.Address,
			Network: s.config.X402Network,
		})
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, errorResponse("failed to create wallet", "internal_error"))
			return
		}
	default:
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to look up wallet", "internal_error"))
		return
	}

	// Reuse the existing token issuance flow — same Paseto, same payload shape,
	// so all downstream middleware and handlers keep working unchanged.
	resp, err := s.issueTokens(ctx, user, org)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse("failed to issue tokens", "internal_error"))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
		"user":          resp.User,
		"org":           resp.Org,
		"wallet": gin.H{
			"address": challenge.Address,
			"network": s.config.X402Network,
		},
	})
}

// walletShorthand returns the first 4 + last 4 chars of a Stellar address,
// e.g. "GAWWHN…GX2UX". Used for human-readable display labels.
func walletShorthand(addr string) string {
	if len(addr) < 12 {
		return addr
	}
	return addr[:4] + "…" + addr[len(addr)-4:]
}
