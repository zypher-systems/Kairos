package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/zypher-systems/kairos/internal/server/metrics"
)

// logRequests is the access log. Every request is logged at Info, except
// probes (/healthz, /metrics) and the agents' claim long-poll, which would
// drown everything else and go to Debug instead.
func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		dur := time.Since(start)
		class := fmt.Sprintf("%dxx", ww.Status()/100)
		metrics.HTTPRequests.Inc(r.Method, class)
		metrics.HTTPLatency.Observe(dur, class)
		level := slog.LevelInfo
		switch r.URL.Path {
		case "/healthz", "/metrics", "/api/v1/agent/claim", "/api/v1/agent/heartbeat":
			level = slog.LevelDebug
		}
		if ww.Status() >= 500 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", dur.Milliseconds(),
			"ip", clientIP(r),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if a.cfg != nil && a.cfg.HTTPS {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json", "err", err)
	}
}

type errBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errBody{Error: msg})
}

// writeErrCode is writeErr with a stable machine-readable code.
func writeErrCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Error: msg, Code: code})
}

func requestID(ctx context.Context) string {
	return middleware.GetReqID(ctx)
}
