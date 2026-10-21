package ascode

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
)

// ExportAll serializes every calendar, secret store, job, and workflow as a YAML bundle.
func ExportAll(ctx context.Context, st *store.Store) ([]byte, error) {
	cals, err := st.ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	stores, err := st.ListSecretStores(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := st.ListAllJobs(ctx)
	if err != nil {
		return nil, err
	}
	wfs, err := st.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	var docs []Document
	for _, c := range cals {
		docs = append(docs, ExportCalendar(c))
	}
	for _, s := range stores {
		docs = append(docs, ExportSecretStore(s))
	}
	for _, j := range jobs {
		d, err := ExportJob(ctx, st, j)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	sort.Slice(wfs, func(i, j int) bool { return wfs[i].Name < wfs[j].Name })
	for _, w := range wfs {
		detail, err := st.GetWorkflowDetail(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		d, err := ExportWorkflow(ctx, st, detail)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return MarshalDocs(docs)
}

// ExportJob builds a Job document. Secret values are omitted.
func ExportJob(ctx context.Context, st *store.Store, j store.Job) (Document, error) {
	enabled := j.Enabled
	doc := Document{
		APIVersion: APIVersion,
		Kind:       KindJob,
		Metadata: Meta{
			Name:        j.Name,
			Description: j.Description,
			Tags:        j.Tags,
			Enabled:     &enabled,
		},
		Spec: Spec{
			Schedule: &ScheduleSpec{
				Type:            j.ScheduleType,
				Cron:            j.CronExpr,
				IntervalSeconds: j.IntervalSeconds,
				At:              formatTime(j.OneoffAt),
				Timezone:        j.Timezone,
			},
			Target:                 exportTarget(ctx, st, j),
			Exec:                   exportExec(ctx, st, j),
			Env:                    j.EnvPlain,
			TimeoutSeconds:         j.TimeoutSeconds,
			SLAMissedGraceSeconds:  j.SLAMissedGraceSeconds,
			SLALateSeconds:         j.SLALateSeconds,
			RequireApproval:        j.RequireApproval,
			ApprovalTimeoutSeconds: j.ApprovalTimeoutSeconds,
			Concurrency:            j.ConcurrencyPolicy,
		},
	}
	if j.RetryCount > 0 || j.RetryBaseDelayMs != 1000 || j.RetryMaxDelayMs != 30000 {
		doc.Spec.Retry = &RetrySpec{
			Count:       j.RetryCount,
			BaseDelayMs: j.RetryBaseDelayMs,
			MaxDelayMs:  j.RetryMaxDelayMs,
		}
	}
	names := make([]string, 0, len(j.EnvSecrets))
	for k := range j.EnvSecrets {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > 0 {
		doc.Spec.Secrets = &SecretSpec{Names: names}
	}
	if len(j.EnvSecretRefs) > 0 {
		refs := make([]SecretRefSpec, 0, len(j.EnvSecretRefs))
		for _, r := range j.EnvSecretRefs {
			refs = append(refs, SecretRefSpec{
				Name:  r.Name,
				Store: st.SecretStoreName(ctx, r.StoreID),
				Path:  r.Path,
				Key:   r.Key,
			})
		}
		doc.Spec.SecretRefs = &refs
	}
	if j.CalendarID != nil {
		if n := st.CalendarName(ctx, *j.CalendarID); n != "" {
			doc.Spec.Calendar = &n
		}
	}
	if len(j.NotifyChannelIDs) > 0 {
		for _, id := range j.NotifyChannelIDs {
			if n := st.ChannelName(ctx, id); n != "" {
				doc.Spec.Notify = append(doc.Spec.Notify, n)
			}
		}
	}
	if j.WatchEnabled {
		w := &WatchSpec{
			Enabled:         true,
			Target:          j.WatchTarget,
			Path:            j.WatchPath,
			IntervalSeconds: j.WatchIntervalSec,
		}
		if j.WatchTarget == "ssh" {
			w.Host = st.SSHHostName(ctx, j.WatchSSHHostID)
		}
		if j.WatchTarget == "agent" && j.WatchAgentID != nil {
			w.Agent = st.AgentName(ctx, *j.WatchAgentID)
		}
		if j.WatchTarget == "location" {
			w.Location = st.StorageLocationName(ctx, j.WatchLocationID)
		}
		doc.Spec.Watch = w
	}
	return doc, nil
}

func exportTarget(ctx context.Context, st *store.Store, j store.Job) *TargetSpec {
	t := &TargetSpec{Type: j.TargetType}
	switch j.TargetType {
	case "ssh":
		t.Host = st.SSHHostName(ctx, j.SSHHostID)
	case "agent":
		var sel struct {
			AgentID string `json:"agent_id"`
		}
		_ = json.Unmarshal(j.TargetSelector, &sel)
		if sel.AgentID != "" {
			if id, err := uuid.Parse(sel.AgentID); err == nil {
				t.Agent = st.AgentName(ctx, id)
			}
		}
	case "selector":
		var labels map[string]string
		_ = json.Unmarshal(j.TargetSelector, &labels)
		t.Labels = labels
	}
	return t
}

func exportExec(ctx context.Context, st *store.Store, j store.Job) *ExecSpec {
	e := &ExecSpec{
		Mode:    j.ExecMode,
		Script:  j.ShellScript,
		Image:   j.DockerImage,
		Command: j.DockerCommand,
		Workdir: j.Workdir,
	}
	if j.ExecMode == "transfer" {
		e.Transfer = &TransferSpec{
			Source:         exportEndpoint(ctx, st, j.TransferConfig.Source),
			Dest:           exportEndpoint(ctx, st, j.TransferConfig.Dest),
			Overwrite:      j.TransferConfig.Overwrite,
			DeleteSource:   j.TransferConfig.DeleteSource,
			PreserveMtime:  j.TransferConfig.PreserveMTime,
			VerifyChecksum: j.TransferConfig.VerifyChecksum,
			FromTrigger:    j.TransferConfig.FromTrigger,
		}
	}
	return e
}

func exportEndpoint(ctx context.Context, st *store.Store, e store.TransferEndpoint) EndpointSpec {
	out := EndpointSpec{Kind: e.Kind, Path: e.Path}
	switch e.Kind {
	case "location":
		out.Location = st.StorageLocationName(ctx, e.LocationID)
	case "library":
		out.File = st.FileName(ctx, e.FileID)
	case "ssh":
		out.Host = st.SSHHostName(ctx, e.SSHHostID)
	case "agent":
		if e.AgentID != nil {
			out.Agent = st.AgentName(ctx, *e.AgentID)
		}
	}
	return out
}

// ExportWorkflow builds a Workflow document with edges named by job.
func ExportWorkflow(ctx context.Context, st *store.Store, d store.WorkflowDetail) (Document, error) {
	enabled := d.Enabled
	names := map[uuid.UUID]string{}
	for _, j := range d.Jobs {
		names[j.ID] = j.Name
	}
	doc := Document{
		APIVersion: APIVersion,
		Kind:       KindWorkflow,
		Metadata: Meta{
			Name:        d.Name,
			Description: d.Description,
			Enabled:     &enabled,
		},
		Spec: Spec{
			Parameters: d.Parameters,
		},
	}
	if d.ScheduleType != "" && d.ScheduleType != "manual" {
		sc := &ScheduleSpec{Type: d.ScheduleType, Cron: d.CronExpr, IntervalSeconds: d.IntervalSeconds, Timezone: d.Timezone}
		if d.OneoffAt != nil {
			sc.At = d.OneoffAt.UTC().Format(time.RFC3339)
		}
		doc.Spec.Schedule = sc
		if d.CalendarID != nil {
			name := st.CalendarName(ctx, *d.CalendarID)
			if name != "" {
				doc.Spec.Calendar = &name
			}
		}
	}
	if d.WatchEnabled {
		doc.Spec.Watch = &WatchSpec{
			Enabled:         true,
			Target:          "location",
			Location:        st.StorageLocationName(ctx, d.WatchLocationID),
			Path:            d.WatchPath,
			IntervalSeconds: d.WatchIntervalSec,
		}
	}
	for _, e := range d.Edges {
		when := e.Condition
		if when == "" {
			when = "success"
		}
		doc.Spec.Edges = append(doc.Spec.Edges, EdgeSpec{
			From: names[e.FromJobID],
			To:   names[e.ToJobID],
			When: when,
		})
	}
	return doc, nil
}

// ExportCalendar builds a Calendar document.
func ExportCalendar(c store.Calendar) Document {
	return Document{
		APIVersion: APIVersion,
		Kind:       KindCalendar,
		Metadata: Meta{
			Name: c.Name,
		},
		Spec: Spec{
			Dates: c.Dates,
		},
	}
}

// ExportSecretStore builds a SecretStore document. The Vault token is omitted.
func ExportSecretStore(s store.SecretStore) Document {
	return Document{
		APIVersion: APIVersion,
		Kind:       KindSecretStore,
		Metadata: Meta{
			Name: s.Name,
		},
		Spec: Spec{
			Type:      s.Type,
			Address:   s.Address,
			Mount:     s.Mount,
			Namespace: s.Namespace,
		},
	}
}
