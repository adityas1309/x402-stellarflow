package sidecar

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type catalogEntry struct {
	Endpoint    string  `json:"endpoint"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	PriceUSDC   float64 `json:"price_usdc"`
	Description string  `json:"description"`
	Network     string  `json:"network"`
	PayTo       string  `json:"pay_to"`
	Asset       string  `json:"asset"`
	AssetCode   string  `json:"asset_code"`
}

// CatalogHandler returns a gin handler that serves the public /api/catalog
// endpoint from the loaded YAML config. Agents and developers use this
// to discover what endpoints are available and how much they cost.
func CatalogHandler(endpoints []EndpointDef, cfg X402Config) gin.HandlerFunc {
	// Pre-build the response once (it doesn't change at runtime).
	catalog := make([]catalogEntry, len(endpoints))
	for i, ep := range endpoints {
		catalog[i] = catalogEntry{
			Endpoint:    ep.Path,
			Method:      ep.Method,
			Path:        ep.Path,
			PriceUSDC:   ep.PriceUSDC,
			Description: ep.Description,
			Network:     cfg.Network,
			PayTo:       cfg.TreasuryAddress,
			Asset:       cfg.USDCContract,
			AssetCode:   cfg.USDCCode,
		}
	}

	return func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"catalog": catalog})
	}
}
