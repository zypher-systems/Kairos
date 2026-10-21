package ascode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zypher-systems/kairos/internal/server/scheduler"
	"github.com/zypher-systems/kairos/internal/server/store"
)

// Change is one create/update/unchanged document in an apply.
type Change struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Action  string   `json:"action"` // create | update | unchanged
	Changes []string `json:"changes,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Result is the apply report.
type Result struct {
	DryRun bool     `json:"dry_run"`
	Items  []Change `json:"items"`
}

// Apply upserts Calendar, SecretStore, then Job, then Workflow documents by name.
func Apply(ctx context.Context, st *store.Store, docs []Document, dryRun bool) (Result, error) {
	out := Result{DryRun: dryRun}
	var cals []Document
	var stores []Document
	var jobs []Document
	var flows []Document
	for _, d := range docs {
		switch d.Kind {
		case KindCalendar:
			cals = append(cals, d)
		case KindSecretStore:
			stores = append(stores, d)
		case KindJob:
			jobs = append(jobs, d)
		case KindWorkflow:
			flows = append(flows, d)
		}
	}
	for _, d := range cals {
		ch, err := applyCalendar(ctx, st, d, dryRun)
		if err != nil {
			ch.Error = err.Error()
			if ch.Kind == "" {
				ch.Kind = KindCalendar
				ch.Name = d.Metadata.Name
			}
		}
		out.Items = append(out.Items, ch)
	}
	for _, d := range stores {
		ch, err := applySecretStore(ctx, st, d, dryRun)
		if err != nil {
			ch.Error = err.Error()
			if ch.Kind == "" {
				ch.Kind = KindSecretStore
				ch.Name = d.Metadata.Name
			}
		}
		out.Items = append(out.Items, ch)
	}
	for _, d := range jobs {
		ch, err := applyJob(ctx, st, d, dryRun)
		if err != nil {
			ch.Error = err.Error()
			if ch.Kind == "" {
				ch.Kind = KindJob
				ch.Name = d.Metadata.Name
			}
		}
		out.Items = append(out.Items, ch)
	}
	for _, d := range flows {
		ch, err := applyWorkflow(ctx, st, d, dryRun)
		if err != nil {
			ch.Error = err.Error()
			if ch.Kind == "" {
				ch.Kind = KindWorkflow
				ch.Name = d.Metadata.Name
			}
		}
		out.Items = append(out.Items, ch)
	}
	return out, nil
}

func applySecretStore(ctx context.Context, st *store.Store, d Document, dryRun bool) (Change, error) {
	ch := Change{Kind: KindSecretStore, Name: d.Metadata.Name}
	want := store.SecretStore{
		Name:      d.Metadata.Name,
		Type:      d.Spec.Type,
		Address:   d.Spec.Address,
		Mount:     d.Spec.Mount,
		Namespace: d.Spec.Namespace,
		Token:     d.Spec.Token,
	}
	if want.Type == "" {
		want.Type = "vault"
	}
	if err := want.Validate(); err != nil {
		return ch, err
	}
	existing, err := st.SecretStoreByName(ctx, d.Metadata.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	found := err == nil
	if !found {
		ch.Action = "create"
		ch.Changes = []string{"new secret store"}
		if dryRun {
			return ch, nil
		}
		return ch, st.CreateSecretStore(ctx, &want)
	}
	want.ID = existing.ID
	if want.Token == "" {
		want.Token = store.SecretMask
	}
	var changes []string
	if existing.Address != want.Address {
		changes = append(changes, "address")
	}
	if existing.Mount != want.Mount && want.Mount != "" {
		changes = append(changes, "mount")
	}
	if existing.Namespace != want.Namespace {
		changes = append(changes, "namespace")
	}
	if d.Spec.Token != "" {
		changes = append(changes, "token")
	}
	if len(changes) == 0 {
		ch.Action = "unchanged"
		return ch, nil
	}
	ch.Action = "update"
	ch.Changes = changes
	if dryRun {
		return ch, nil
	}
	return ch, st.UpdateSecretStore(ctx, &want)
}

func applyCalendar(ctx context.Context, st *store.Store, d Document, dryRun bool) (Change, error) {
	ch := Change{Kind: KindCalendar, Name: d.Metadata.Name}
	want := store.Calendar{
		Name:  d.Metadata.Name,
		Dates: d.Spec.Dates,
	}
	if err := want.Validate(); err != nil {
		return ch, err
	}
	existing, err := st.CalendarByName(ctx, d.Metadata.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	found := err == nil
	if !found {
		ch.Action = "create"
		ch.Changes = []string{"new calendar"}
		if dryRun {
			return ch, nil
		}
		return ch, st.CreateCalendar(ctx, &want)
	}
	want.ID = existing.ID
	if reflect.DeepEqual(existing.Dates, want.Dates) {
		ch.Action = "unchanged"
		return ch, nil
	}
	ch.Action = "update"
	ch.Changes = []string{"dates"}
	if dryRun {
		return ch, nil
	}
	return ch, st.UpdateCalendar(ctx, &want)
}

func applyJob(ctx context.Context, st *store.Store, d Document, dryRun bool) (Change, error) {
	ch := Change{Kind: KindJob, Name: d.Metadata.Name}
	want, err := jobFromDoc(ctx, st, d)
	if err != nil {
		return ch, err
	}
	existing, err := st.JobByName(ctx, d.Metadata.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	found := err == nil
	if found {
		want.ID = existing.ID
		want.WorkflowID = existing.WorkflowID
		mergeSecretsOnUpdate(&want, existing)
		if d.Spec.SecretRefs == nil {
			want.EnvSecretRefs = existing.EnvSecretRefs
		}
		if d.Spec.Calendar == nil {
			want.CalendarID = existing.CalendarID
		}
	}
	if err := want.Validate(); err != nil {
		return ch, err
	}
	if err := scheduler.ValidateJob(&want); err != nil {
		return ch, err
	}
	if !found {
		ch.Action = "create"
		ch.Changes = []string{"new job"}
		if dryRun {
			return ch, nil
		}
		next, ok, err := scheduler.InitialNextRun(&want, time.Now())
		if err != nil {
			return ch, err
		}
		var nextPtr *time.Time
		if ok {
			nextPtr = &next
		}
		_, err = st.CreateJob(ctx, &want, nextPtr)
		return ch, err
	}
	diff := diffJob(existing, want)
	if len(diff) == 0 {
		ch.Action = "unchanged"
		return ch, nil
	}
	ch.Action = "update"
	ch.Changes = diff
	if dryRun {
		return ch, nil
	}
	saved, err := st.UpdateJob(ctx, &want)
	if err != nil {
		return ch, err
	}
	next, ok, err := scheduler.InitialNextRun(&saved, time.Now())
	if err != nil {
		return ch, err
	}
	var nextPtr *time.Time
	if ok {
		nextPtr = &next
	}
	return ch, st.SetJobNextRun(ctx, saved.ID, nextPtr)
}

func applyWorkflow(ctx context.Context, st *store.Store, d Document, dryRun bool) (Change, error) {
	ch := Change{Kind: KindWorkflow, Name: d.Metadata.Name}
	enabled := enabledOf(d.Metadata, true)
	params := d.Spec.Parameters
	if params == nil {
		params = map[string]string{}
	}
	edges, err := resolveEdges(ctx, st, d.Spec.Edges)
	if err != nil {
		return ch, err
	}
	existing, err := st.WorkflowByName(ctx, d.Metadata.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	found := err == nil
	if !found {
		ch.Action = "create"
		ch.Changes = []string{"new workflow"}
		if dryRun {
			return ch, nil
		}
		wf := store.Workflow{
			Name:        d.Metadata.Name,
			Description: d.Metadata.Description,
			Enabled:     enabled,
			Parameters:  params,
		}
		if err := applyWorkflowSchedule(ctx, st, &wf, d); err != nil {
			return ch, err
		}
		if err := st.CreateWorkflow(ctx, &wf); err != nil {
			return ch, err
		}
		if err := st.ReplaceWorkflowEdges(ctx, wf.ID, edges); err != nil {
			return ch, err
		}
		return ch, st.SetJobsWorkflowFromEdges(ctx, wf.ID, edges)
	}
	var changes []string
	if existing.Description != d.Metadata.Description {
		changes = append(changes, fmt.Sprintf("description: %q → %q", existing.Description, d.Metadata.Description))
	}
	if existing.Enabled != enabled {
		changes = append(changes, fmt.Sprintf("enabled: %v → %v", existing.Enabled, enabled))
	}
	if !reflect.DeepEqual(existing.Parameters, params) {
		changes = append(changes, "parameters")
	}
	cur, err := st.ListWorkflowEdges(ctx, existing.ID)
	if err != nil {
		return ch, err
	}
	if !edgesEqual(cur, edges) {
		changes = append(changes, "edges")
	}
	want := existing
	if err := applyWorkflowSchedule(ctx, st, &want, d); err != nil {
		return ch, err
	}
	if want.ScheduleType != existing.ScheduleType || want.CronExpr != existing.CronExpr || want.IntervalSeconds != existing.IntervalSeconds ||
		want.Timezone != existing.Timezone || !equalTimePtr(want.OneoffAt, existing.OneoffAt) || !equalUUIDPtr(want.CalendarID, existing.CalendarID) {
		changes = append(changes, "schedule")
	}
	if want.WatchEnabled != existing.WatchEnabled || want.WatchPath != existing.WatchPath ||
		want.WatchIntervalSec != existing.WatchIntervalSec || !equalUUIDPtr(want.WatchLocationID, existing.WatchLocationID) {
		changes = append(changes, "watch")
	}
	if len(changes) == 0 {
		ch.Action = "unchanged"
		return ch, nil
	}
	ch.Action = "update"
	ch.Changes = changes
	if dryRun {
		return ch, nil
	}
	existing = want
	existing.Name = d.Metadata.Name
	existing.Description = d.Metadata.Description
	existing.Enabled = enabled
	existing.Parameters = params
	if err := st.UpdateWorkflow(ctx, &existing); err != nil {
		return ch, err
	}
	if err := st.ReplaceWorkflowEdges(ctx, existing.ID, edges); err != nil {
		return ch, err
	}
	return ch, st.SetJobsWorkflowFromEdges(ctx, existing.ID, edges)
}

// applyWorkflowSchedule fills the workflow's schedule and trigger from a
// document and computes next_run_at.
func applyWorkflowSchedule(ctx context.Context, st *store.Store, wf *store.Workflow, d Document) error {
	wf.ScheduleType, wf.CronExpr, wf.IntervalSeconds, wf.OneoffAt, wf.CalendarID = "manual", "", 0, nil, nil
	if wf.Timezone == "" {
		wf.Timezone = "UTC"
	}
	if sc := d.Spec.Schedule; sc != nil {
		wf.ScheduleType = sc.Type
		wf.CronExpr = sc.Cron
		wf.IntervalSeconds = sc.IntervalSeconds
		if sc.Timezone != "" {
			wf.Timezone = sc.Timezone
		}
		at, err := parseTime(sc.At)
		if err != nil {
			return err
		}
		wf.OneoffAt = at
	}
	if d.Spec.Calendar != nil && *d.Spec.Calendar != "" {
		c, err := st.CalendarByName(ctx, *d.Spec.Calendar)
		if err != nil {
			return nameErr("calendar", *d.Spec.Calendar, err)
		}
		id := c.ID
		wf.CalendarID = &id
	}
	wf.WatchEnabled, wf.WatchLocationID, wf.WatchPath = false, nil, ""
	if w := d.Spec.Watch; w != nil && w.Enabled {
		l, err := st.StorageLocationByName(ctx, w.Location)
		if err != nil {
			return nameErr("storage location", w.Location, err)
		}
		id := l.ID
		wf.WatchEnabled, wf.WatchLocationID, wf.WatchPath, wf.WatchIntervalSec = true, &id, w.Path, w.IntervalSeconds
	}
	if err := scheduler.ValidateSchedule(scheduler.WorkflowSchedule(wf)); err != nil {
		return err
	}
	sc := scheduler.WorkflowSchedule(wf)
	sc.NextRunAt = nil
	next, ok, err := scheduler.InitialNextFor(sc, time.Now())
	if err != nil {
		return err
	}
	wf.NextRunAt = nil
	if ok {
		wf.NextRunAt = &next
	}
	return nil
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func equalUUIDPtr(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func resolveEdges(ctx context.Context, st *store.Store, specs []EdgeSpec) ([]store.WorkflowEdge, error) {
	var out []store.WorkflowEdge
	for i, e := range specs {
		if e.From == "" || e.To == "" {
			return nil, fmt.Errorf("edge %d: from and to are required", i)
		}
		from, err := st.JobByName(ctx, e.From)
		if err != nil {
			return nil, fmt.Errorf("edge from %q: %w", e.From, nameErr("job", e.From, err))
		}
		to, err := st.JobByName(ctx, e.To)
		if err != nil {
			return nil, fmt.Errorf("edge to %q: %w", e.To, nameErr("job", e.To, err))
		}
		when := e.When
		if when == "" {
			when = "success"
		}
		out = append(out, store.WorkflowEdge{
			FromJobID: from.ID,
			ToJobID:   to.ID,
			Condition: when,
		})
	}
	return out, nil
}

func edgesEqual(a, b []store.WorkflowEdge) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(e store.WorkflowEdge) string {
		return e.FromJobID.String() + "|" + e.ToJobID.String() + "|" + e.Condition
	}
	seen := map[string]int{}
	for _, e := range a {
		seen[key(e)]++
	}
	for _, e := range b {
		k := key(e)
		if seen[k] == 0 {
			return false
		}
		seen[k]--
	}
	return true
}

func jobFromDoc(ctx context.Context, st *store.Store, d Document) (store.Job, error) {
	j := store.Job{
		Name:                   d.Metadata.Name,
		Description:            d.Metadata.Description,
		Tags:                   d.Metadata.Tags,
		Enabled:                enabledOf(d.Metadata, true),
		EnvPlain:               d.Spec.Env,
		TimeoutSeconds:         d.Spec.TimeoutSeconds,
		SLAMissedGraceSeconds:  d.Spec.SLAMissedGraceSeconds,
		SLALateSeconds:         d.Spec.SLALateSeconds,
		RequireApproval:        d.Spec.RequireApproval,
		ApprovalTimeoutSeconds: d.Spec.ApprovalTimeoutSeconds,
		ConcurrencyPolicy:      d.Spec.Concurrency,
	}
	if j.EnvPlain == nil {
		j.EnvPlain = map[string]string{}
	}
	if d.Spec.Secrets != nil {
		j.EnvSecrets = d.Spec.Secrets.valueMap()
	}
	if d.Spec.Schedule != nil {
		s := d.Spec.Schedule
		j.ScheduleType = s.Type
		j.CronExpr = s.Cron
		j.IntervalSeconds = s.IntervalSeconds
		j.Timezone = s.Timezone
		at, err := parseTime(s.At)
		if err != nil {
			return j, err
		}
		j.OneoffAt = at
	}
	if d.Spec.Target != nil {
		if err := applyTarget(ctx, st, &j, *d.Spec.Target); err != nil {
			return j, err
		}
	}
	if d.Spec.Exec != nil {
		if err := applyExec(ctx, st, &j, *d.Spec.Exec); err != nil {
			return j, err
		}
	}
	if d.Spec.Retry != nil {
		j.RetryCount = d.Spec.Retry.Count
		j.RetryBaseDelayMs = d.Spec.Retry.BaseDelayMs
		j.RetryMaxDelayMs = d.Spec.Retry.MaxDelayMs
	}
	if len(d.Spec.Notify) > 0 {
		for _, name := range d.Spec.Notify {
			ch, err := st.ChannelByName(ctx, name)
			if err != nil {
				return j, nameErr("channel", name, err)
			}
			j.NotifyChannelIDs = append(j.NotifyChannelIDs, ch.ID)
		}
	}
	if d.Spec.Watch != nil && d.Spec.Watch.Enabled {
		w := d.Spec.Watch
		j.WatchEnabled = true
		j.WatchTarget = w.Target
		j.WatchPath = w.Path
		j.WatchIntervalSec = w.IntervalSeconds
		if w.Target == "location" {
			l, err := st.StorageLocationByName(ctx, w.Location)
			if err != nil {
				return j, nameErr("storage location", w.Location, err)
			}
			id := l.ID
			j.WatchLocationID = &id
		}
		if w.Target == "ssh" {
			h, err := st.SSHHostByName(ctx, w.Host)
			if err != nil {
				return j, nameErr("ssh host", w.Host, err)
			}
			id := h.ID
			j.WatchSSHHostID = &id
		}
		if w.Target == "agent" {
			a, err := st.AgentByName(ctx, w.Agent)
			if err != nil {
				return j, nameErr("agent", w.Agent, err)
			}
			id := a.ID
			j.WatchAgentID = &id
		}
	}
	if d.Spec.SecretRefs != nil {
		for _, r := range *d.Spec.SecretRefs {
			ss, err := st.SecretStoreByName(ctx, r.Store)
			if err != nil {
				return j, nameErr("secret store", r.Store, err)
			}
			j.EnvSecretRefs = append(j.EnvSecretRefs, store.EnvSecretRef{
				Name:    r.Name,
				StoreID: ss.ID,
				Path:    r.Path,
				Key:     r.Key,
			})
		}
	}
	if d.Spec.Calendar != nil {
		name := strings.TrimSpace(*d.Spec.Calendar)
		if name != "" {
			cal, err := st.CalendarByName(ctx, name)
			if err != nil {
				return j, nameErr("calendar", name, err)
			}
			id := cal.ID
			j.CalendarID = &id
		}
	}
	return j, nil
}

func applyTarget(ctx context.Context, st *store.Store, j *store.Job, t TargetSpec) error {
	j.TargetType = t.Type
	switch t.Type {
	case "local", "":
		j.TargetType = "local"
		j.TargetSelector = json.RawMessage(`{}`)
	case "ssh":
		h, err := st.SSHHostByName(ctx, t.Host)
		if err != nil {
			return nameErr("ssh host", t.Host, err)
		}
		id := h.ID
		j.SSHHostID = &id
	case "agent":
		a, err := st.AgentByName(ctx, t.Agent)
		if err != nil {
			return nameErr("agent", t.Agent, err)
		}
		raw, _ := json.Marshal(map[string]string{"agent_id": a.ID.String()})
		j.TargetSelector = raw
	case "selector":
		if len(t.Labels) == 0 {
			return errors.New("target.labels required for selector")
		}
		raw, _ := json.Marshal(t.Labels)
		j.TargetSelector = raw
	default:
		return fmt.Errorf("unknown target type %q", t.Type)
	}
	return nil
}

func applyExec(ctx context.Context, st *store.Store, j *store.Job, e ExecSpec) error {
	j.ExecMode = e.Mode
	j.ShellScript = e.Script
	j.DockerImage = e.Image
	j.DockerCommand = e.Command
	j.Workdir = e.Workdir
	if e.Mode == "transfer" {
		if e.Transfer == nil {
			return errors.New("exec.transfer is required for transfer jobs")
		}
		src, err := resolveEndpoint(ctx, st, e.Transfer.Source)
		if err != nil {
			return fmt.Errorf("transfer source: %w", err)
		}
		dst, err := resolveEndpoint(ctx, st, e.Transfer.Dest)
		if err != nil {
			return fmt.Errorf("transfer dest: %w", err)
		}
		j.TransferConfig = store.TransferConfig{
			Source:         src,
			Dest:           dst,
			Overwrite:      e.Transfer.Overwrite,
			DeleteSource:   e.Transfer.DeleteSource,
			PreserveMTime:  e.Transfer.PreserveMtime,
			VerifyChecksum: e.Transfer.VerifyChecksum,
			FromTrigger:    e.Transfer.FromTrigger,
		}
	}
	return nil
}

func resolveEndpoint(ctx context.Context, st *store.Store, e EndpointSpec) (store.TransferEndpoint, error) {
	out := store.TransferEndpoint{Kind: e.Kind, Path: e.Path}
	switch e.Kind {
	case "location":
		l, err := st.StorageLocationByName(ctx, e.Location)
		if err != nil {
			return out, nameErr("storage location", e.Location, err)
		}
		id := l.ID
		out.LocationID = &id
	case "library":
		if e.File != "" {
			f, err := st.FileByName(ctx, e.File)
			if err != nil {
				return out, nameErr("file", e.File, err)
			}
			id := f.ID
			out.FileID = &id
		}
	case "ssh":
		h, err := st.SSHHostByName(ctx, e.Host)
		if err != nil {
			return out, nameErr("ssh host", e.Host, err)
		}
		id := h.ID
		out.SSHHostID = &id
	case "agent":
		a, err := st.AgentByName(ctx, e.Agent)
		if err != nil {
			return out, nameErr("agent", e.Agent, err)
		}
		id := a.ID
		out.AgentID = &id
	}
	return out, nil
}

func mergeSecretsOnUpdate(want *store.Job, existing store.Job) {
	if want.EnvSecrets == nil {
		want.EnvSecrets = map[string]string{}
		for k := range existing.EnvSecrets {
			want.EnvSecrets[k] = store.SecretMask
		}
		return
	}
	for k, v := range want.EnvSecrets {
		if v == "" {
			want.EnvSecrets[k] = store.SecretMask
		}
	}
}

func diffJob(a, b store.Job) []string {
	var out []string
	add := func(field string, left, right any) {
		if !reflect.DeepEqual(left, right) {
			out = append(out, fmt.Sprintf("%s: %v → %v", field, left, right))
		}
	}
	add("description", a.Description, b.Description)
	add("schedule_type", a.ScheduleType, b.ScheduleType)
	add("cron", a.CronExpr, b.CronExpr)
	add("interval_seconds", a.IntervalSeconds, b.IntervalSeconds)
	add("timezone", a.Timezone, b.Timezone)
	add("target_type", a.TargetType, b.TargetType)
	add("exec_mode", a.ExecMode, b.ExecMode)
	add("shell_script", a.ShellScript, b.ShellScript)
	add("docker_image", a.DockerImage, b.DockerImage)
	add("workdir", a.Workdir, b.Workdir)
	add("enabled", a.Enabled, b.Enabled)
	add("timeout_seconds", a.TimeoutSeconds, b.TimeoutSeconds)
	add("sla_missed_grace_seconds", a.SLAMissedGraceSeconds, b.SLAMissedGraceSeconds)
	add("sla_late_seconds", a.SLALateSeconds, b.SLALateSeconds)
	add("require_approval", a.RequireApproval, b.RequireApproval)
	add("approval_timeout_seconds", a.ApprovalTimeoutSeconds, b.ApprovalTimeoutSeconds)
	add("concurrency", a.ConcurrencyPolicy, b.ConcurrencyPolicy)
	add("retry_count", a.RetryCount, b.RetryCount)
	if !reflect.DeepEqual(sorted(a.Tags), sorted(b.Tags)) {
		out = append(out, "tags")
	}
	if !reflect.DeepEqual(a.EnvPlain, b.EnvPlain) {
		out = append(out, "env")
	}
	if !secretKeysEqual(a.EnvSecrets, b.EnvSecrets) {
		out = append(out, "secrets")
	}
	if !bytesEqual(a.TargetSelector, b.TargetSelector) {
		out = append(out, "target")
	}
	if a.SSHHostID != nil || b.SSHHostID != nil {
		if fmt.Sprint(a.SSHHostID) != fmt.Sprint(b.SSHHostID) {
			out = append(out, "ssh_host")
		}
	}
	if !reflect.DeepEqual(a.DockerCommand, b.DockerCommand) {
		out = append(out, "docker_command")
	}
	if !reflect.DeepEqual(a.TransferConfig, b.TransferConfig) {
		out = append(out, "transfer")
	}
	if a.WatchEnabled != b.WatchEnabled || a.WatchPath != b.WatchPath || a.WatchTarget != b.WatchTarget {
		out = append(out, "watch")
	}
	if !uuidSliceEqual(a.NotifyChannelIDs, b.NotifyChannelIDs) {
		out = append(out, "notify")
	}
	if !reflect.DeepEqual(a.EnvSecretRefs, b.EnvSecretRefs) {
		out = append(out, "secret_refs")
	}
	if fmt.Sprint(a.CalendarID) != fmt.Sprint(b.CalendarID) {
		out = append(out, "calendar")
	}
	return out
}

func secretKeysEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func bytesEqual(a, b []byte) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return string(a) == string(b)
}

func uuidSliceEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[uuid.UUID]int{}
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		if seen[id] == 0 {
			return false
		}
		seen[id]--
	}
	return true
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func nameErr(kind, name string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MissingName(kind, name)
	}
	return err
}
