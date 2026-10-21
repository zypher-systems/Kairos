// Package metrics is a small in-process registry of counters and gauges
// rendered in the Prometheus text format. It keeps Kairos free of a client
// library while giving operators the numbers that matter: what the
// scheduler, dispatcher, agents and notifier are doing right now.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type labelled struct {
	mu    sync.Mutex
	vals  map[string]*atomic.Int64 // encoded labels -> value
	fvals map[string]*atomic.Value // encoded labels -> float64 (gauges set)
}

// Counter only goes up. Labels are fixed per metric and given in order.
type Counter struct {
	name, help string
	labels     []string
	l          labelled
}

// Gauge is set to a value.
type Gauge struct {
	name, help string
	labels     []string
	l          labelled
}

// Timing accumulates a _sum and _count pair (a summary without quantiles).
type Timing struct {
	name, help string
	labels     []string
	sum        labelled
	count      labelled
}

var (
	regMu    sync.Mutex
	counters []*Counter
	gauges   []*Gauge
	timings  []*Timing
	// Collectors run at render time for values that live elsewhere (database
	// counts, pool stats).
	collectors []func(w io.Writer)
)

// NewCounter registers a counter.
func NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{name: name, help: help, labels: labels}
	c.l.vals = map[string]*atomic.Int64{}
	regMu.Lock()
	counters = append(counters, c)
	regMu.Unlock()
	return c
}

// NewGauge registers a gauge.
func NewGauge(name, help string, labels ...string) *Gauge {
	g := &Gauge{name: name, help: help, labels: labels}
	g.l.fvals = map[string]*atomic.Value{}
	regMu.Lock()
	gauges = append(gauges, g)
	regMu.Unlock()
	return g
}

// NewTiming registers a duration summary.
func NewTiming(name, help string, labels ...string) *Timing {
	t := &Timing{name: name, help: help, labels: labels}
	t.sum.vals = map[string]*atomic.Int64{}
	t.count.vals = map[string]*atomic.Int64{}
	regMu.Lock()
	timings = append(timings, t)
	regMu.Unlock()
	return t
}

// AddCollector registers a function that writes fully formed exposition
// lines at render time.
func AddCollector(fn func(w io.Writer)) {
	regMu.Lock()
	collectors = append(collectors, fn)
	regMu.Unlock()
}

func encode(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		v := ""
		if i < len(values) {
			v = values[i]
		}
		fmt.Fprintf(&b, "%s=%q", n, v)
	}
	b.WriteByte('}')
	return b.String()
}

func (l *labelled) int(key string) *atomic.Int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.vals[key]
	if !ok {
		v = &atomic.Int64{}
		l.vals[key] = v
	}
	return v
}

// Add increments the counter for the given label values.
func (c *Counter) Add(n int64, values ...string) {
	c.l.int(encode(c.labels, values)).Add(n)
}

// Inc adds one.
func (c *Counter) Inc(values ...string) { c.Add(1, values...) }

// Set stores the gauge value for the given label values.
func (g *Gauge) Set(v float64, values ...string) {
	key := encode(g.labels, values)
	g.l.mu.Lock()
	av, ok := g.l.fvals[key]
	if !ok {
		av = &atomic.Value{}
		g.l.fvals[key] = av
	}
	g.l.mu.Unlock()
	av.Store(v)
}

// Observe records one duration.
func (t *Timing) Observe(d time.Duration, values ...string) {
	key := encode(t.labels, values)
	t.sum.int(key).Add(d.Microseconds())
	t.count.int(key).Add(1)
}

// Write renders every registered metric in the Prometheus text format.
func Write(w io.Writer) {
	regMu.Lock()
	cs := append([]*Counter(nil), counters...)
	gs := append([]*Gauge(nil), gauges...)
	ts := append([]*Timing(nil), timings...)
	cols := make([]func(io.Writer), len(collectors))
	copy(cols, collectors)
	regMu.Unlock()

	for _, c := range cs {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
		c.l.mu.Lock()
		keys := sortedKeys(c.l.vals)
		for _, k := range keys {
			fmt.Fprintf(w, "%s%s %d\n", c.name, k, c.l.vals[k].Load())
		}
		if len(keys) == 0 && len(c.labels) == 0 {
			fmt.Fprintf(w, "%s 0\n", c.name)
		}
		c.l.mu.Unlock()
	}
	for _, g := range gs {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.name, g.help, g.name)
		g.l.mu.Lock()
		keys := make([]string, 0, len(g.l.fvals))
		for k := range g.l.fvals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, _ := g.l.fvals[k].Load().(float64)
			fmt.Fprintf(w, "%s%s %s\n", g.name, k, formatFloat(v))
		}
		g.l.mu.Unlock()
	}
	for _, t := range ts {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s summary\n", t.name, t.help, t.name)
		t.count.mu.Lock()
		keys := sortedKeys(t.count.vals)
		t.count.mu.Unlock()
		for _, k := range keys {
			sum := float64(t.sum.int(k).Load()) / 1e6
			fmt.Fprintf(w, "%s_sum%s %s\n%s_count%s %d\n", t.name, k, formatFloat(sum), t.name, k, t.count.int(k).Load())
		}
	}
	for _, fn := range cols {
		fn(w)
	}
}

func sortedKeys(m map[string]*atomic.Int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

// The metrics Kairos maintains in-process. Names are stable; dashboards
// may depend on them.
var (
	RunsStarted  = NewCounter("kairos_runs_started_total", "Runs that began executing, by target type.", "target")
	RunsFinished = NewCounter("kairos_runs_finished_total", "Runs that reached a final state, by status.", "status")
	RunsRequeued = NewCounter("kairos_runs_requeued_total", "Runs handed back to the queue after a lost lease, shutdown or agent release.", "reason")
	RunsFenced   = NewCounter("kairos_runs_fenced_total", "Runs a worker killed because its claim was lost.")

	SchedulerTicks   = NewCounter("kairos_scheduler_ticks_total", "Scheduler ticks, by outcome.", "outcome")
	SchedulerLastOK  = NewGauge("kairos_scheduler_last_tick_timestamp_seconds", "Unix time of the last successful scheduler tick on this replica (0 when not leader).")
	SchedulerEnqueue = NewCounter("kairos_scheduler_enqueued_total", "Runs enqueued by the scheduler.")

	NotifySent   = NewCounter("kairos_notify_delivered_total", "Notifications delivered, by channel type.", "type")
	NotifyFailed = NewCounter("kairos_notify_failed_total", "Notification delivery attempts that failed, by channel type.", "type")
	NotifyDead   = NewGauge("kairos_notify_dead_letters", "Notifications that exhausted their attempts and await an operator.")
	OutboxDepth  = NewGauge("kairos_notify_outbox_pending", "Notifications waiting for delivery.")

	AgentsOnline = NewGauge("kairos_agents_online", "Agents that heartbeated within the timeout.")

	HTTPRequests = NewCounter("kairos_http_requests_total", "HTTP requests, by method and status class.", "method", "class")
	HTTPLatency  = NewTiming("kairos_http_request_duration_seconds", "HTTP request latency.", "class")

	DBPool = NewGauge("kairos_db_pool_connections", "Connection pool state.", "state")
)
