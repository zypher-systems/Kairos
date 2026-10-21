package ascode

import (
	"strings"
	"testing"
)

func TestParseAndMarshalJob(t *testing.T) {
	raw := []byte(`apiVersion: kairos/v1
kind: Job
metadata:
  name: echo
  enabled: true
spec:
  schedule:
    type: interval
    interval_seconds: 60
    timezone: UTC
  target:
    type: local
  exec:
    mode: shell
    script: echo hi
  env:
    FOO: bar
  secrets:
    - DB_PASS
`)
	docs, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Kind != KindJob || docs[0].Metadata.Name != "echo" {
		t.Fatalf("parse: %+v", docs)
	}
	if docs[0].Spec.Exec == nil || docs[0].Spec.Exec.Script != "echo hi" {
		t.Fatalf("exec: %+v", docs[0].Spec.Exec)
	}
	if docs[0].Spec.Secrets == nil || len(docs[0].Spec.Secrets.Names) != 1 {
		t.Fatalf("secrets: %+v", docs[0].Spec.Secrets)
	}
	out, err := MarshalDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseBytes(out)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Metadata.Name != "echo" {
		t.Fatalf("round-trip name %q", again[0].Metadata.Name)
	}
	if !strings.Contains(string(out), "kind: Job") {
		t.Fatalf("marshal missing kind: %s", out)
	}
}

func TestParseWorkflowAndSecretMap(t *testing.T) {
	raw := []byte(`apiVersion: kairos/v1
kind: Workflow
metadata:
  name: nightly
spec:
  parameters:
    REGION: us-east
  edges:
    - from: extract
      to: load
      when: success
---
apiVersion: kairos/v1
kind: Job
metadata:
  name: extract
spec:
  secrets:
    TOKEN: s3cret
`)
	docs, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("want 2 docs got %d", len(docs))
	}
	if docs[0].Spec.Parameters["REGION"] != "us-east" {
		t.Fatalf("params %+v", docs[0].Spec.Parameters)
	}
	if docs[1].Spec.Secrets.valueMap()["TOKEN"] != "s3cret" {
		t.Fatalf("secret map %+v", docs[1].Spec.Secrets)
	}
}

func TestParseSecretStore(t *testing.T) {
	raw := []byte(`apiVersion: kairos/v1
kind: SecretStore
metadata:
  name: prod-vault
spec:
  type: vault
  address: https://vault.example:8200
  mount: secret
  token: s.x
`)
	docs, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Kind != KindSecretStore || docs[0].Spec.Address == "" {
		t.Fatalf("parse: %+v", docs)
	}
}

func TestParseCalendar(t *testing.T) {
	raw := []byte(`apiVersion: kairos/v1
kind: Calendar
metadata:
  name: us-holidays
spec:
  dates:
    - 2026-12-25
    - 2027-01-01
`)
	docs, err := ParseBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Kind != KindCalendar || len(docs[0].Spec.Dates) != 2 {
		t.Fatalf("parse: %+v", docs)
	}
}
