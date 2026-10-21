package store

import (
	"encoding/json"
	"testing"
)

func TestJobValidateSelector(t *testing.T) {
	j := &Job{
		Name:            "sel",
		ScheduleType:    "interval",
		IntervalSeconds: 60,
		Timezone:        "UTC",
		TargetType:      "selector",
		TargetSelector:  json.RawMessage(`{}`),
		ExecMode:        "shell",
		ShellScript:     "true",
	}
	if err := j.Validate(); err == nil {
		t.Fatal("empty selector must fail")
	}

	j.TargetSelector = json.RawMessage(`{"role":"worker"}`)
	if err := j.Validate(); err != nil {
		t.Fatalf("labeled selector rejected: %v", err)
	}
}

func TestJobValidateAgentRequiresID(t *testing.T) {
	j := &Job{
		Name:            "agent",
		ScheduleType:    "interval",
		IntervalSeconds: 60,
		Timezone:        "UTC",
		TargetType:      "agent",
		TargetSelector:  json.RawMessage(`{}`),
		ExecMode:        "shell",
	}
	if err := j.Validate(); err == nil {
		t.Fatal("agent without agent_id must fail")
	}
}
