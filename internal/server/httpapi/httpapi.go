// Package httpapi wires the HTTP surface: middleware, REST routes,
// WebSocket hub, and backed services.
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zypher-systems/kairos/internal/server/config"
	"github.com/zypher-systems/kairos/internal/server/notify"
	"github.com/zypher-systems/kairos/internal/server/oidcauth"
	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
	kversion "github.com/zypher-systems/kairos/internal/version"
)

// API bundles the configured services and handlers for the server.
type API struct {
	cfg        *config.Config
	pool       *pgxpool.Pool
	store      *store.Store
	hub        *ws.Hub
	notifier   *notify.Notifier
	loginLimit *loginLimiter
	oidc       oidcauth.Provider
	leader     LeaderView
}

// LeaderView is the scheduler-lease status exposed on /info and /healthz.
type LeaderView interface {
	InstanceID() string
	IsLeader() bool
	LeaseUntil() time.Time
}

// New builds the API service container.
func New(cfg *config.Config, pool *pgxpool.Pool, st *store.Store, hub *ws.Hub, notifier *notify.Notifier) *API {
	return &API{
		cfg:        cfg,
		pool:       pool,
		store:      st,
		hub:        hub,
		notifier:   notifier,
		loginLimit: newLoginLimiter(10, time.Minute),
		oidc:       oidcauth.NewLive(),
	}
}

// SetOIDCProvider replaces the OIDC client (tests).
func (a *API) SetOIDCProvider(p oidcauth.Provider) {
	a.oidc = p
}

// SetLeader attaches scheduler-lease status for /info and /healthz.
func (a *API) SetLeader(v LeaderView) {
	a.leader = v
}

// Router assembles the full HTTP handler tree.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(a.trustedRealIP)
	r.Use(middleware.Recoverer)
	r.Use(a.logRequests)
	r.Use(a.securityHeaders)
	r.Use(a.guardOrigin)

	r.Get("/healthz", a.handleHealth)
	r.Get("/metrics", a.handleMetrics)
	// Browser sessions authenticate websockets via the session cookie.
	r.With(a.AdminAuth).Get("/api/v1/ws", a.hub.ServeHTTP)

	r.Route("/api/v1", func(r chi.Router) {
		// Public: identity & status.
		r.Get("/info", a.handleInfo)
		r.Get("/auth/status", a.handleAuthStatus)
		r.Get("/openapi.json", a.handleOpenAPI)

		// Public: first-run setup + login/logout.
		r.Post("/auth/setup", a.handleAuthSetup)
		r.Post("/auth/login", a.handleAuthLogin)
		r.Post("/auth/logout", a.handleAuthLogout)
		r.Post("/auth/sso/start", a.handleSSOStart)
		r.Get("/auth/sso/callback", a.handleSSOCallback)

		// Public: agent installer + binaries (no secrets; tokens travel separately).
		r.Get("/agent/install.sh", a.handleAgentInstallScript)
		r.Get("/agent/download/{os}/{arch}", a.handleAgentDownload)

		// Agent worker surface — token-authenticated, no session needed.
		r.Group(func(r chi.Router) {
			r.Use(a.AgentAuth)
			r.Post("/agent/register", a.handleAgentRegister)
			r.Post("/agent/heartbeat", a.handleAgentHeartbeat)
			r.Post("/agent/claim", a.handleAgentClaim)
			r.Post("/agent/runs/{id}/logs", a.handleAgentLogs)
			r.Post("/agent/runs/{id}/lease", a.handleAgentLease)
			r.Post("/agent/runs/{id}/complete", a.handleAgentComplete)
			r.Post("/agent/watch-event", a.handleAgentWatchEvent)
			r.Get("/agent/files/{id}/content", a.handleAgentFileDownload)
			r.Post("/agent/files", a.handleAgentFileUpload)
		})

		// Authenticated surface. Viewers may read; writes nest under operator/admin.
		r.Group(func(r chi.Router) {
			r.Use(a.AdminAuth)

			r.Post("/auth/password", a.handleAuthPassword)

			r.Get("/jobs", a.handleListJobs)
			r.Get("/jobs/{id}", a.handleGetJob)
			r.Get("/runs", a.handleListRuns)
			r.Get("/runs/{id}", a.handleGetRun)
			r.Get("/runs/{id}/logs", a.handleGetRunLogs)
			r.Get("/logs/search", a.handleLogSearch)
			r.Get("/settings", a.handleGetSettings)
			r.Get("/stats", a.handleStats)
			r.Get("/audit", a.handleAuditLog)
			r.Get("/tokens", a.handleListAPITokens)
			r.Get("/agents", a.handleListAgents)
			r.Get("/agent-server-url", a.handleAgentServerURL)
			r.Get("/ssh-hosts", a.handleListSSHHosts)
			r.Get("/ssh-keys", a.handleListSSHKeys)
			r.Get("/notifications", a.handleListChannels)
			r.Get("/notifications/dead-letters", a.handleListDeadLetters)
			r.Get("/secret-stores", a.handleListSecretStores)
			r.Get("/storage-locations", a.handleListStorageLocations)
			r.Get("/calendars", a.handleListCalendars)
			r.Get("/workflows", a.handleListWorkflows)
			r.Get("/workflows/{id}", a.handleGetWorkflow)
			r.Get("/files", a.handleListFiles)
			r.Get("/files/{id}/download", a.handleDownloadFile)
			r.Get("/files/{id}/content", a.handleViewFile)
			r.Get("/export", a.handleExport)

			r.Group(func(r chi.Router) {
				r.Use(a.RequireRole(store.RoleOperator))

				r.Post("/jobs", a.handleCreateJob)
				r.Put("/jobs/{id}", a.handleUpdateJob)
				r.Delete("/jobs/{id}", a.handleDeleteJob)
				r.Post("/jobs/{id}/run", a.handleRunJob)
				r.Post("/runs/{id}/cancel", a.handleCancelRun)
				r.Post("/runs/{id}/approve", a.handleApproveRun)
				r.Post("/runs/{id}/reject", a.handleRejectRun)

				r.Post("/tokens", a.handleCreateAPIToken)
				r.Delete("/tokens/{id}", a.handleDeleteAPIToken)

				r.Post("/agents", a.handleCreateAgent)
				r.Delete("/agents/{id}", a.handleDeleteAgent)
				r.Put("/agents/{id}/labels", a.handleUpdateAgentLabels)

				r.Post("/ssh-hosts", a.handleCreateSSHHost)
				r.Put("/ssh-hosts/{id}", a.handleUpdateSSHHost)
				r.Delete("/ssh-hosts/{id}", a.handleDeleteSSHHost)
				r.Post("/ssh-hosts/{id}/test", a.handleTestSSHHost)
				r.Post("/ssh-hosts/{id}/push-install", a.handlePushInstallAgent)

				r.Post("/ssh-keys", a.handleCreateSSHKey)
				r.Delete("/ssh-keys/{id}", a.handleDeleteSSHKey)
				r.Post("/ssh-keys/{id}/push", a.handlePushSSHKey)

				r.Post("/notifications", a.handleCreateChannel)
				r.Put("/notifications/{id}", a.handleUpdateChannel)
				r.Delete("/notifications/{id}", a.handleDeleteChannel)
				r.Post("/notifications/{id}/test", a.handleTestChannel)
				r.Post("/notifications/dead-letters/{id}/retry", a.handleRetryDeadLetter)
				r.Delete("/notifications/dead-letters/{id}", a.handleDeleteDeadLetter)

				r.Post("/storage-locations", a.handleCreateStorageLocation)
				r.Put("/storage-locations/{id}", a.handleUpdateStorageLocation)
				r.Delete("/storage-locations/{id}", a.handleDeleteStorageLocation)
				r.Post("/storage-locations/{id}/test", a.handleTestStorageLocation)

				r.Post("/secret-stores", a.handleCreateSecretStore)
				r.Put("/secret-stores/{id}", a.handleUpdateSecretStore)
				r.Delete("/secret-stores/{id}", a.handleDeleteSecretStore)

				r.Post("/calendars", a.handleCreateCalendar)
				r.Put("/calendars/{id}", a.handleUpdateCalendar)
				r.Delete("/calendars/{id}", a.handleDeleteCalendar)

				r.Post("/workflows", a.handleCreateWorkflow)
				r.Put("/workflows/{id}", a.handleUpdateWorkflow)
				r.Delete("/workflows/{id}", a.handleDeleteWorkflow)
				r.Put("/workflows/{id}/edges", a.handleReplaceWorkflowEdges)
				r.Post("/workflows/{id}/run", a.handleRunWorkflow)

				r.Post("/files", a.handleUploadFile)
				r.Delete("/files/{id}", a.handleDeleteFile)

				r.Post("/apply", a.handleApply)
				r.Post("/git-sync", a.handleGitSync)
			})

			r.Group(func(r chi.Router) {
				r.Use(a.RequireRole(store.RoleAdmin))

				r.Put("/settings", a.handleUpdateSettings)

				r.Get("/users", a.handleListUsers)
				r.Post("/users", a.handleCreateUser)
				r.Put("/users/{id}", a.handleUpdateUser)
				r.Delete("/users/{id}", a.handleDeleteUser)
			})
		})
	})

	// SPA fallback (only when a web build is embedded).
	if spa := newSPA(); spa != nil {
		r.NotFound(spa.ServeHTTP)
	}

	return r
}

// schedulerStaleAfter is how long the leader may go without a successful
// scheduler tick before /healthz reports degraded.
const schedulerStaleAfter = 30 * time.Second

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy"})
		return
	}
	body := map[string]any{"status": "healthy"}
	a.writeLeader(body)
	// The leader must have ticked recently: a scheduler stuck on a bad
	// batch or a stalled transaction is not healthy even when the database
	// answers.
	if a.leader != nil && a.leader.IsLeader() {
		if last := scheduler.LastTick(); !last.IsZero() {
			age := time.Since(last)
			body["scheduler_tick_age_seconds"] = int(age.Seconds())
			if age > schedulerStaleAfter {
				body["status"] = "degraded"
				body["detail"] = "the scheduler has not completed a tick recently"
				writeJSON(w, http.StatusServiceUnavailable, body)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *API) handleInfo(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"name":    "kairos",
		"version": kversion.Version,
	}
	a.writeLeader(body)
	writeJSON(w, http.StatusOK, body)
}

func (a *API) writeLeader(body map[string]any) {
	body["leader"] = false
	body["instance_id"] = ""
	if a.leader == nil {
		return
	}
	body["instance_id"] = a.leader.InstanceID()
	body["leader"] = a.leader.IsLeader()
	if until := a.leader.LeaseUntil(); !until.IsZero() {
		body["lease_until"] = until.UTC().Format(time.RFC3339)
	}
}
