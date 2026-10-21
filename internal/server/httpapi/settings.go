package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (a *API) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Settings
		RetentionRuns          *int    `json:"retention_runs"`
		RetentionDays          *int    `json:"retention_days"`
		LibraryMaxAgeDays      *int    `json:"library_max_age_days"`
		GitSyncEnabled         *bool   `json:"git_sync_enabled"`
		GitSyncIntervalSeconds *int    `json:"git_sync_interval_seconds"`
		GitSyncToken           *string `json:"git_sync_token"`
		SSOEnabled             *bool   `json:"sso_enabled"`
		SSOClientSecret        *string `json:"sso_client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if in.DefaultTimezone != "" {
		if _, err := time.LoadLocation(in.DefaultTimezone); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid default_timezone")
			return
		}
	}
	cur, err := a.store.SettingsForWrite(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	if in.DefaultTimezone != "" {
		cur.DefaultTimezone = in.DefaultTimezone
	}
	if in.RetentionRuns != nil && *in.RetentionRuns > 0 {
		cur.RetentionRuns = *in.RetentionRuns
	}
	if in.RetentionDays != nil && *in.RetentionDays > 0 {
		cur.RetentionDays = *in.RetentionDays
	}
	if in.LibraryMaxAgeDays != nil && *in.LibraryMaxAgeDays >= 0 {
		cur.LibraryMaxAgeDays = *in.LibraryMaxAgeDays
	}
	if in.GitSyncEnabled != nil {
		cur.GitSyncEnabled = *in.GitSyncEnabled
		cur.GitSyncURL = strings.TrimSpace(in.GitSyncURL)
	}
	if strings.TrimSpace(in.GitSyncURL) != "" {
		cur.GitSyncURL = strings.TrimSpace(in.GitSyncURL)
	}
	if in.GitSyncRef != "" {
		cur.GitSyncRef = in.GitSyncRef
	}
	if in.GitSyncPath != "" {
		cur.GitSyncPath = in.GitSyncPath
	}
	if in.GitSyncIntervalSeconds != nil && *in.GitSyncIntervalSeconds >= 60 {
		cur.GitSyncIntervalSeconds = *in.GitSyncIntervalSeconds
	}
	if in.GitSyncToken != nil {
		tok := strings.TrimSpace(*in.GitSyncToken)
		if tok != "" && tok != store.SecretMask {
			enc, err := a.store.SealGitSyncToken(tok)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "failed to store git token")
				return
			}
			cur.GitSyncTokenEnc = enc
		}
	}
	cur.GitSyncToken = ""
	if in.SSOEnabled != nil {
		cur.SSOEnabled = *in.SSOEnabled
	}
	if strings.TrimSpace(in.SSOIssuer) != "" || in.SSOEnabled != nil {
		cur.SSOIssuer = strings.TrimSpace(in.SSOIssuer)
	}
	if in.SSOClientID != "" || in.SSOEnabled != nil {
		cur.SSOClientID = strings.TrimSpace(in.SSOClientID)
	}
	if in.SSORedirectURL != "" || in.SSOEnabled != nil {
		cur.SSORedirectURL = strings.TrimSpace(in.SSORedirectURL)
	}
	if in.SSOClientSecret != nil {
		sec := strings.TrimSpace(*in.SSOClientSecret)
		if sec != "" && sec != store.SecretMask {
			enc, err := a.store.SealSSOClientSecret(sec)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "failed to store SSO client secret")
				return
			}
			cur.SSOClientSecretEnc = enc
		}
	}
	cur.SSOClientSecret = ""
	if err := a.store.SaveSettings(r.Context(), cur); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	out, err := a.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	a.audit(r, "settings.update", "settings", "", out.DefaultTimezone)
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	counts, err := a.store.RunCounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load stats")
		return
	}
	writeJSON(w, http.StatusOK, counts)
}
