package api

import (
	"net/http"
	"strings"

	"github.com/your-org/stellarflow/internal/token"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const authorizationPayloadKey = "authorization_payload"

// authMiddleware validates the PASETO bearer token
func authMiddleware(tokenMaker token.Maker) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		authHeader := ctx.GetHeader("Authorization")
		if len(authHeader) == 0 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("authorization header is not provided", "unauthorized"))
			return
		}

		fields := strings.Fields(authHeader)
		if len(fields) < 2 {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("invalid authorization header format", "unauthorized"))
			return
		}

		authType := strings.ToLower(fields[0])
		if authType != "bearer" {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("unsupported authorization type", "unauthorized"))
			return
		}

		accessToken := fields[1]
		payload, err := tokenMaker.VerifyToken(accessToken)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse(err.Error(), "unauthorized"))
			return
		}

		ctx.Set(authorizationPayloadKey, payload)
		ctx.Next()
	}
}

// getAuthPayload retrieves the token payload from the gin context
func getAuthPayload(ctx *gin.Context) *token.Payload {
	payload, _ := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	return payload
}

// requireRole returns middleware that checks the user has one of the given roles.
// Super admins bypass role checks.
func requireRole(roles ...string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		payload := getAuthPayload(ctx)
		// Super admins bypass role checks
		if payload.IsSuperAdmin {
			ctx.Next()
			return
		}
		for _, r := range roles {
			if payload.Role == r {
				ctx.Next()
				return
			}
		}
		ctx.AbortWithStatusJSON(http.StatusForbidden, errorResponse("insufficient permissions", "forbidden"))
	}
}

// ErrorLoggingMiddleware logs 5xx errors using logrus
func ErrorLoggingMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Next()

		status := ctx.Writer.Status()
		if status >= 500 {
			logrus.WithFields(logrus.Fields{
				"status": status,
				"method": ctx.Request.Method,
				"path":   ctx.Request.URL.Path,
				"ip":     ctx.ClientIP(),
			}).Error("server error")
		}
	}
}
