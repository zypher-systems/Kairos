package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zypher-systems/kairos/internal/server/ascode"
	"github.com/zypher-systems/kairos/internal/server/gitsync"
)

// GET /api/v1/export — YAML bundle of jobs and workflows.
func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
	raw, err := ascode.ExportAll(r.Context(), a.store)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to export")
		return
	}
	a.audit(r, "ascode.export", "jobs", "", "")
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="kairos.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// POST /api/v1/apply — upsert YAML. { yaml, dry_run }
func (a *API) handleApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		YAML   string `json:"yaml"`
		DryRun bool   `json:"dry_run"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(in.YAML) == "" {
		writeErr(w, http.StatusBadRequest, "yaml is required")
		return
	}
	docs, err := ascode.ParseBytes([]byte(in.YAML))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := ascode.Apply(r.Context(), a.store, docs, in.DryRun)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	action := "ascode.apply"
	if in.DryRun {
		action = "ascode.dry_run"
	}
	a.audit(r, action, "jobs", "", applyAudit(res))
	writeJSON(w, http.StatusOK, res)
}

// POST /api/v1/git-sync — one-shot pull + apply.
func (a *API) handleGitSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	_ = decodeJSON(r, &in)
	res, err := gitsync.SyncOnce(r.Context(), a.store, in.DryRun)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "ascode.git_sync", "git", "", applyAudit(res))
	writeJSON(w, http.StatusOK, res)
}

func applyAudit(res ascode.Result) string {
	var created, updated, failed int
	for _, it := range res.Items {
		if it.Error != "" {
			failed++
			continue
		}
		switch it.Action {
		case "create":
			created++
		case "update":
			updated++
		}
	}
	return fmt.Sprintf("create=%d update=%d failed=%d dry_run=%v", created, updated, failed, res.DryRun)
}
