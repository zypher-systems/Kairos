package ascode

import (
	"context"
	"testing"
	"time"

	"github.com/zypher-systems/kairos/internal/testutil"
)

func TestApplyDryRunAndUpsert(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()

	raw := []byte(`apiVersion: kairos/v1
kind: Job
metadata:
  name: ascode-echo
  enabled: false
spec:
  schedule:
    type: interval
    interval_seconds: 3600
    timezone: UTC
  target:
    type: local
  exec:
    mode: shell
    script: echo first
`)
	docs, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := Apply(ctx, st, docs, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Items) != 1 || dry.Items[0].Action != "create" {
		t.Fatalf("dry-run: %+v", dry.Items)
	}
	if _, err := st.JobByName(ctx, "ascode-echo"); err == nil {
		t.Fatal("dry-run must not persist")
	}

	live, err := Apply(ctx, st, docs, false)
	if err != nil {
		t.Fatal(err)
	}
	if live.Items[0].Action != "create" {
		t.Fatalf("apply: %+v", live.Items)
	}
	got, err := st.JobByName(ctx, "ascode-echo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellScript != "echo first" {
		t.Fatalf("script %q", got.ShellScript)
	}

	docs[0].Spec.Exec.Script = "echo second"
	again, err := Apply(ctx, st, docs, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Items[0].Action != "update" {
		t.Fatalf("update: %+v", again.Items)
	}
	got, err = st.JobByName(ctx, "ascode-echo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellScript != "echo second" {
		t.Fatalf("updated script %q", got.ShellScript)
	}

	bundle, err := ExportAll(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseBytes(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) == 0 || back[0].Metadata.Name != "ascode-echo" {
		t.Fatalf("export parse: %+v", back)
	}

	unchanged, err := Apply(ctx, st, back, true)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Items[0].Action != "unchanged" && unchanged.Items[0].Error != "" {
		t.Fatalf("export/apply dry-run: %+v", unchanged.Items)
	}
	_ = time.Now
}

func TestApplyCalendarAndJob(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	docs, err := ParseBytes([]byte(`
apiVersion: kairos/v1
kind: Calendar
metadata:
  name: us-holidays
spec:
  dates: ["2026-12-25"]
---
apiVersion: kairos/v1
kind: Job
metadata:
  name: nightly
  enabled: false
spec:
  calendar: us-holidays
  schedule: { type: interval, interval_seconds: 3600, timezone: UTC }
  target: { type: local }
  exec: { mode: shell, script: true }
`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, docs, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.Error != "" {
			t.Fatalf("apply error: %+v", it)
		}
	}
	job, err := st.JobByName(ctx, "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if job.CalendarID == nil {
		t.Fatal("job should attach calendar")
	}
	bundle, err := ExportAll(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseBytes(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) < 2 || back[0].Kind != KindCalendar || back[1].Kind != KindJob {
		t.Fatalf("export order %+v", back)
	}
	if back[1].Spec.Calendar == nil || *back[1].Spec.Calendar != "us-holidays" {
		t.Fatalf("export calendar %+v", back[1].Spec.Calendar)
	}
}

func TestApplyWorkflowParameters(t *testing.T) {
	st := testutil.Store(t)
	ctx := context.Background()
	docs, err := ParseBytes([]byte(`
apiVersion: kairos/v1
kind: Job
metadata:
  name: wf-a
  enabled: false
spec:
  schedule: { type: interval, interval_seconds: 3600, timezone: UTC }
  target: { type: local }
  exec: { mode: shell, script: true }
---
apiVersion: kairos/v1
kind: Job
metadata:
  name: wf-b
  enabled: false
spec:
  schedule: { type: interval, interval_seconds: 3600, timezone: UTC }
  target: { type: local }
  exec: { mode: shell, script: true }
---
apiVersion: kairos/v1
kind: Workflow
metadata:
  name: chain
spec:
  parameters:
    REGION: us-east
  edges:
    - from: wf-a
      to: wf-b
      when: always
`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, docs, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.Error != "" {
			t.Fatalf("apply error: %+v", it)
		}
	}
	wf, err := st.WorkflowByName(ctx, "chain")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Parameters["REGION"] != "us-east" {
		t.Fatalf("params %+v", wf.Parameters)
	}
	execID, err := st.CreateWorkflowExecution(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.JobByName(ctx, "wf-b")
	if err != nil {
		t.Fatal(err)
	}
	st.OverlayWorkflowParams(ctx, &job, &execID)
	if job.EnvPlain["REGION"] != "us-east" {
		t.Fatalf("overlay %+v", job.EnvPlain)
	}
}
