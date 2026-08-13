package api

import (
	"time"

	db "github.com/your-org/stellarflow/internal/db/sqlc"

	"github.com/gin-gonic/gin"
)

// authResponse is the shared shape returned by SEP-10 sign-in.
// The original codebase also returned this from email/password login,
// register, and refresh — those endpoints have been removed for the
// hackathon: SEP-10 is the only sign-in path.

type authResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	User         userPayload `json:"user"`
	Org          orgPayload  `json:"org"`
}

type userPayload struct {
	ID           int64  `json:"id"`
	OrgID        int64  `json:"org_id"`
	Email        string `json:"email"`
	FullName     string `json:"full_name"`
	Role         string `json:"role"`
	IsSuperAdmin bool   `json:"is_super_admin"`
	CreatedAt    string `json:"created_at"`
}

type orgPayload struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	LogoURL string `json:"logo_url"`
	Plan    string `json:"plan"`
	Credits int32  `json:"credits"`
}

// issueTokens creates a Paseto access + refresh token pair for the given
// user/org and persists the refresh token in the database. Reused by SEP-10
// after a successful wallet challenge verification.
func (s *Server) issueTokens(ctx *gin.Context, user db.User, org db.Organization) (authResponse, error) {
	accessToken, _, err := s.tokenMaker.CreateToken(
		user.Email, user.ID, org.ID, user.Role, user.IsSuperAdmin,
		s.config.AccessTokenDuration, true,
	)
	if err != nil {
		return authResponse{}, err
	}

	refreshToken, refreshPayload, err := s.tokenMaker.CreateToken(
		user.Email, user.ID, org.ID, user.Role, user.IsSuperAdmin,
		s.config.RefreshTokenDuration, false,
	)
	if err != nil {
		return authResponse{}, err
	}

	_, err = s.store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		Token:     refreshToken,
		ExpiresAt: refreshPayload.ExpiredAt,
	})
	if err != nil {
		return authResponse{}, err
	}

	logoURL := ""
	if org.LogoURL.Valid {
		logoURL = org.LogoURL.String
	}

	return authResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User: userPayload{
			ID:           user.ID,
			OrgID:        user.OrgID,
			Email:        user.Email,
			FullName:     user.FullName,
			Role:         user.Role,
			IsSuperAdmin: user.IsSuperAdmin,
			CreatedAt:    user.CreatedAt.Format(time.RFC3339),
		},
		Org: orgPayload{
			ID:      org.ID,
			Name:    org.Name,
			Slug:    org.Slug,
			LogoURL: logoURL,
			Plan:    org.Plan,
			Credits: org.Credits,
		},
	}, nil
}
