// Package notify delivers run-completion events to notification channels:
// generic webhooks (HMAC-signed), Discord, Slack, and SMTP email.
// Deliveries are durable in a Postgres outbox so a restart cannot drop them.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/zypher-systems/kairos/internal/server/metrics"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// Event kinds.
const (
	EventRunFailed    = "run_failed"
	EventRunSucceeded = "run_succeeded"
	EventRunMissed    = "run_missed"
	EventRunLate      = "run_late"
	EventRunPending   = "run_pending"
	EventRunRejected  = "run_rejected"
	EventRunCanceled  = "run_canceled"
)

// Event is one notification trigger.
type Event struct {
	Kind            string `json:"event"`
	Timestamp       string `json:"timestamp"`
	RunID           string `json:"run_id"`
	JobID           string `json:"job_id"`
	JobName         string `json:"job_name"`
	Status          string `json:"status"`
	Trigger         string `json:"trigger"`
	TargetType      string `json:"target_type"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	Error           string `json:"error,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
	URL             string `json:"url,omitempty"`
	Attempt         int    `json:"attempt"`
	// DeliveryID identifies this outbox row. It is the same on every retry
	// of the same event, so receivers can de-duplicate.
	DeliveryID string `json:"delivery_id,omitempty"`
}

// Notifier fans events out to subscribed channels via a Postgres outbox.
// Claimed rows are locked in the database, so several replicas can run a
// notifier without sending the same event twice, and rows are delivered
// per channel in parallel: a slow or dead endpoint holds up its own
// channel's queue, nobody else's.
type Notifier struct {
	store     *store.Store
	publicURL string
	client    *http.Client
	workers   int
}

// Delivery timeout for one attempt; the outbox lock is held a little longer.
const (
	deliveryTimeout = 15 * time.Second
	outboxLock      = 2 * deliveryTimeout
)

// New builds a notifier; publicURL prefixes run links in payloads (may be "").
func New(st *store.Store, publicURL string) *Notifier {
	return &Notifier{
		store:     st,
		publicURL: strings.TrimRight(publicURL, "/"),
		client:    newSafeHTTPClient(deliveryTimeout),
		workers:   8,
	}
}

// Run polls the durable outbox until ctx is done.
func (n *Notifier) Run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	gauge := time.NewTicker(15 * time.Second)
	defer gauge.Stop()
	slog.Info("notifier started", "backend", "outbox", "workers", n.workers)
	n.updateGauges(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("notifier stopped")
			return
		case <-t.C:
			n.drain(ctx)
		case <-gauge.C:
			n.updateGauges(ctx)
		}
	}
}

func (n *Notifier) updateGauges(ctx context.Context) {
	pending, dead, err := n.store.OutboxCounts(ctx)
	if err != nil {
		return
	}
	metrics.OutboxDepth.Set(float64(pending))
	metrics.NotifyDead.Set(float64(dead))
}

// drain claims due rows, groups them by channel, and delivers each channel's
// rows in order on its own goroutine, up to n.workers channels at once.
func (n *Notifier) drain(ctx context.Context) {
	rows, err := n.store.ClaimOutboxDue(ctx, 64, outboxLock)
	if err != nil {
		slog.Warn("notify: claim outbox", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	byChannel := map[string][]store.NotifyOutboxRow{}
	var order []string
	for _, r := range rows {
		k := r.ChannelID.String()
		if _, ok := byChannel[k]; !ok {
			order = append(order, k)
		}
		byChannel[k] = append(byChannel[k], r)
	}
	sem := make(chan struct{}, n.workers)
	var wg sync.WaitGroup
	for _, k := range order {
		batch := byChannel[k]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			for _, row := range batch {
				if ctx.Err() != nil {
					return
				}
				n.deliver(ctx, row)
			}
		}()
	}
	wg.Wait()
}

// deliver makes one attempt for one row and records the outcome.
func (n *Notifier) deliver(ctx context.Context, row store.NotifyOutboxRow) {
	ch, err := n.store.GetChannelForDelivery(ctx, row.ChannelID)
	if err != nil {
		_ = n.store.FailOutbox(ctx, row.ID, row.Attempt+1, err.Error())
		return
	}
	var e Event
	if err := json.Unmarshal(row.Payload, &e); err != nil {
		_ = n.store.FailOutbox(ctx, row.ID, row.Attempt+1, err.Error())
		return
	}
	e.DeliveryID = row.ID.String()
	if n.publicURL != "" && e.URL == "" && e.RunID != "" {
		e.URL = n.publicURL + "/runs/" + e.RunID
	}
	sendCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	err = n.send(sendCtx, ch, e)
	cancel()
	if err != nil {
		metrics.NotifyFailed.Inc(ch.Type)
		slog.Warn("notify: delivery failed",
			"channel", ch.Name, "type", ch.Type,
			"attempt", row.Attempt+1, "err", err)
		_ = n.store.FailOutbox(ctx, row.ID, row.Attempt+1, err.Error())
		return
	}
	metrics.NotifySent.Inc(ch.Type)
	_ = n.store.CompleteOutbox(ctx, row.ID)
}

// Test sends a synthetic event synchronously and returns any error directly
// (for the channel "test" button).
func (n *Notifier) Test(ctx context.Context, ch store.NotificationChannel) error {
	e := Event{
		Kind:            "test",
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		RunID:           "00000000-0000-0000-0000-000000000000",
		JobID:           "00000000-0000-0000-0000-000000000000",
		JobName:         "kairos test notification",
		Status:          "succeeded",
		Trigger:         "manual",
		TargetType:      "local",
		DurationSeconds: 1,
		Attempt:         1,
	}
	if n.publicURL != "" {
		e.URL = n.publicURL
	}
	return n.send(ctx, ch, e)
}

// send dispatches one event to one channel.
func (n *Notifier) send(ctx context.Context, ch store.NotificationChannel, e Event) error {
	switch ch.Type {
	case "webhook":
		return n.sendWebhook(ctx, ch, e)
	case "discord":
		return n.sendChat(ctx, ch.Config["webhook_url"], discordBody(e))
	case "slack":
		return n.sendChat(ctx, ch.Config["webhook_url"], slackBody(e))
	case "smtp":
		return n.sendSMTP(ctx, ch, e)
	default:
		return fmt.Errorf("unknown channel type %q", ch.Type)
	}
}

// ----- webhook (generic, HMAC-signed) -----

func (n *Notifier) sendWebhook(ctx context.Context, ch store.NotificationChannel, e Event) error {
	if err := CheckHTTPURL(ch.Config["url"]); err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.Config["url"], bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kairos-notify/1.0")
	ts := fmt.Sprint(time.Now().Unix())
	req.Header.Set("X-Kairos-Timestamp", ts)
	if e.DeliveryID != "" {
		req.Header.Set("X-Kairos-Delivery", e.DeliveryID)
	}
	if secret := ch.Config["secret"]; secret != "" {
		// X-Kairos-Signature covers the body alone and stays for existing
		// receivers. X-Kairos-Signature-V2 also covers the timestamp, so a
		// captured request cannot be replayed later: verify
		// HMAC-SHA256(secret, "<timestamp>.<body>") and reject old timestamps.
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-Kairos-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		mac2 := hmac.New(sha256.New, []byte(secret))
		mac2.Write([]byte(ts))
		mac2.Write([]byte("."))
		mac2.Write(body)
		req.Header.Set("X-Kairos-Signature-V2", "t="+ts+",v2="+hex.EncodeToString(mac2.Sum(nil)))
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// ----- chat webhooks (Discord / Slack) -----

func (n *Notifier) sendChat(ctx context.Context, url string, body []byte) error {
	if err := CheckHTTPURL(url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kairos-notify/1.0")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("chat webhook returned %s", resp.Status)
	}
	return nil
}

func eventHeadline(e Event) string {
	switch e.Kind {
	case "test":
		return "Kairos test notification"
	case EventRunSucceeded:
		return "Run succeeded: " + e.JobName
	case EventRunMissed:
		return "Run missed: " + e.JobName
	case EventRunLate:
		return "Run late: " + e.JobName
	case EventRunPending:
		return "Run pending approval: " + e.JobName
	case EventRunRejected:
		return "Run rejected: " + e.JobName
	case EventRunCanceled:
		return "Run canceled: " + e.JobName
	default:
		return "Run failed: " + e.JobName
	}
}

func eventColor(e Event) int {
	switch e.Kind {
	case EventRunSucceeded, "test":
		return 0x35d08a
	case EventRunLate, EventRunPending:
		return 0xf0b429
	default:
		return 0xe26d7e
	}
}

func discordBody(e Event) []byte {
	fields := []map[string]any{
		{"name": "Status", "value": e.Status, "inline": true},
		{"name": "Target", "value": e.TargetType, "inline": true},
		{"name": "Trigger", "value": e.Trigger, "inline": true},
	}
	if e.ExitCode != nil {
		fields = append(fields, map[string]any{"name": "Exit code", "value": fmt.Sprint(*e.ExitCode), "inline": true})
	}
	if e.DurationSeconds > 0 {
		fields = append(fields, map[string]any{"name": "Duration", "value": fmt.Sprintf("%ds", e.DurationSeconds), "inline": true})
	}
	if e.Error != "" {
		fields = append(fields, map[string]any{"name": "Error", "value": truncate(e.Error, 200)})
	}
	embed := map[string]any{
		"title":     eventHeadline(e),
		"color":     eventColor(e),
		"fields":    fields,
		"timestamp": e.Timestamp,
	}
	if e.URL != "" {
		embed["url"] = e.URL
	}
	body, _ := json.Marshal(map[string]any{"embeds": []any{embed}})
	return body
}

func slackBody(e Event) []byte {
	fields := []map[string]any{
		{"title": "Status", "value": e.Status, "short": true},
		{"title": "Target", "value": e.TargetType, "short": true},
		{"title": "Trigger", "value": e.Trigger, "short": true},
	}
	if e.Error != "" {
		fields = append(fields, map[string]any{"title": "Error", "value": truncate(e.Error, 200), "short": false})
	}
	attachment := map[string]any{
		"color":  fmt.Sprintf("#%06x", eventColor(e)),
		"title":  eventHeadline(e),
		"fields": fields,
		"ts":     time.Now().Unix(),
	}
	if e.URL != "" {
		attachment["title_link"] = e.URL
	}
	body, _ := json.Marshal(map[string]any{"attachments": []any{attachment}})
	return body
}

// ----- SMTP -----

func (n *Notifier) sendSMTP(ctx context.Context, ch store.NotificationChannel, e Event) error {
	cfg := ch.Config
	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(cfg["host"], port)
	if err := CheckDialTarget(cfg["host"]); err != nil {
		return err
	}

	var b bytes.Buffer
	to := strings.Split(cfg["to"], ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	fmt.Fprintf(&b, "From: %s\r\n", headerValue(cfg["from"]))
	fmt.Fprintf(&b, "To: %s\r\n", headerValue(strings.Join(to, ", ")))
	fmt.Fprintf(&b, "Subject: [kairos] %s\r\n", headerValue(eventHeadline(e)))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n\r\n", eventHeadline(e))
	fmt.Fprintf(&b, "job:      %s\r\n", e.JobName)
	fmt.Fprintf(&b, "run:      %s\r\n", e.RunID)
	fmt.Fprintf(&b, "status:   %s\r\n", e.Status)
	fmt.Fprintf(&b, "trigger:  %s\r\n", e.Trigger)
	fmt.Fprintf(&b, "target:   %s\r\n", e.TargetType)
	if e.ExitCode != nil {
		fmt.Fprintf(&b, "exit:     %d\r\n", *e.ExitCode)
	}
	if e.DurationSeconds > 0 {
		fmt.Fprintf(&b, "duration: %ds\r\n", e.DurationSeconds)
	}
	if e.Error != "" {
		fmt.Fprintf(&b, "error:    %s\r\n", truncate(e.Error, 500))
	}
	if e.URL != "" {
		fmt.Fprintf(&b, "link:     %s\r\n", e.URL)
	}

	var auth smtp.Auth
	if cfg["username"] != "" || cfg["password"] != "" {
		auth = smtp.PlainAuth("", cfg["username"], cfg["password"], cfg["host"])
	}

	// "starttls" requires the upgrade: a relay that does not offer it fails
	// the delivery rather than sending credentials and content in clear.
	// "none" is the explicit opt-out for a trusted local relay.
	mode := cfg["tls"]
	if mode == "" {
		mode = "starttls"
	}
	// Honor context cancellation with a watchdog closing the connection.
	switch mode {
	case "ssl":
		conn, err := tls.DialWithDialer(safeDialer(cfg["host"], 10*time.Second), "tcp", addr, &tls.Config{ServerName: cfg["host"]})
		if err != nil {
			return err
		}
		go func() { <-ctx.Done(); conn.Close() }()
		c, err := smtp.NewClient(conn, cfg["host"])
		if err != nil {
			return err
		}
		defer c.Close()
		return smtpSend(c, auth, cfg["from"], to, b.Bytes())
	case "starttls", "none":
		conn, err := safeDialer(cfg["host"], 10*time.Second).DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		go func() { <-ctx.Done(); conn.Close() }()
		c, err := smtp.NewClient(conn, cfg["host"])
		if err != nil {
			return err
		}
		defer c.Close()
		if mode == "starttls" {
			if ok, _ := c.Extension("STARTTLS"); !ok {
				return fmt.Errorf("smtp server %s does not offer STARTTLS; set tls to \"none\" only for a trusted local relay", cfg["host"])
			}
			if err := c.StartTLS(&tls.Config{ServerName: cfg["host"]}); err != nil {
				return err
			}
		}
		return smtpSend(c, auth, cfg["from"], to, b.Bytes())
	default:
		return fmt.Errorf("unknown tls mode %q", mode)
	}
}

func smtpSend(c *smtp.Client, auth smtp.Auth, from string, to []string, msg []byte) error {
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
