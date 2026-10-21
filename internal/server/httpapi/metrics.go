package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/zypher-systems/kairos/internal/server/metrics"
	"github.com/zypher-systems/kairos/internal/version"
)

// GET /metrics — Prometheus text exposition. In-process counters and gauges
// come from the metrics registry; a few database gauges are read on each
// scrape (they use indexes and cost a few milliseconds). When
// KAIROS_METRICS_TOKEN is set the scrape must carry it as a bearer token.
func (a *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if a.cfg != nil && a.cfg.MetricsToken != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.MetricsToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kairos metrics"`)
			writeErr(w, http.StatusUnauthorized, "metrics token required")
			return
		}
	}
	ctx := r.Context()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP kairos_info Build identity.\n# TYPE kairos_info gauge\nkairos_info{version=%q} 1\n", version.Version)
	if a.leader != nil {
		n := 0
		if a.leader.IsLeader() {
			n = 1
		}
		fmt.Fprintf(w, "# HELP kairos_ha_leader 1 if this replica holds the scheduler lease.\n# TYPE kairos_ha_leader gauge\nkairos_ha_leader %d\n", n)
	}
	if counts, err := a.store.RunCounts(ctx); err == nil {
		fmt.Fprintf(w, "# HELP kairos_runs Runs by state (queued, running) and last-24h outcome.\n# TYPE kairos_runs gauge\n")
		for _, key := range []string{"queued", "running", "day_succeeded", "day_failed"} {
			n := 0
			switch t := counts[key].(type) {
			case int:
				n = t
			case int64:
				n = int(t)
			}
			// Kept for existing dashboards.
			fmt.Fprintf(w, "kairos_runs_%s %d\n", key, n)
		}
	}
	if pending, dead, err := a.store.OutboxCounts(ctx); err == nil {
		metrics.OutboxDepth.Set(float64(pending))
		metrics.NotifyDead.Set(float64(dead))
	}
	if n, err := a.store.CountOnlineAgents(ctx); err == nil {
		metrics.AgentsOnline.Set(float64(n))
	}
	if a.pool != nil {
		st := a.pool.Stat()
		metrics.DBPool.Set(float64(st.TotalConns()), "total")
		metrics.DBPool.Set(float64(st.IdleConns()), "idle")
		metrics.DBPool.Set(float64(st.AcquiredConns()), "acquired")
		metrics.DBPool.Set(float64(st.MaxConns()), "max")
	}
	metrics.Write(w)
}
