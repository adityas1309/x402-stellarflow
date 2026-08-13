package sidecar

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
)

// ReverseProxyHandler returns a gin handler that proxies the request to the
// target backend. The request is forwarded as-is (including body, query params,
// and most headers) with the X-Payment header stripped and the payer address
// injected as X-Payer-Address.
func ReverseProxyHandler(targetURL string) (gin.HandlerFunc, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy target URL %q: %w", targetURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom director: rewrite the request to point at the target.
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip the X-Payment header so the target doesn't see it.
		req.Header.Del("X-Payment")
		// Inject the payer address (set by the x402 middleware).
		if payer := req.Header.Get("X-Payer-Address-Internal"); payer != "" {
			req.Header.Set("X-Payer-Address", payer)
			req.Header.Del("X-Payer-Address-Internal")
		}
	}

	return func(ctx *gin.Context) {
		// Pass the payer address through a temporary header.
		if payer, ok := ctx.Get("payer_address"); ok {
			ctx.Request.Header.Set("X-Payer-Address-Internal", payer.(string))
		}
		proxy.ServeHTTP(ctx.Writer, ctx.Request)
	}, nil
}
