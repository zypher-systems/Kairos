package exec

import (
	"strings"
	"testing"

	"github.com/zypher-systems/kairos/internal/server/store"
)

func TestBuildRemoteDockerScriptTrapsStop(t *testing.T) {
	script := buildRemoteDockerScript(store.Job{
		DockerImage:   "alpine:3.20",
		DockerCommand: []string{"echo", "hi"},
	}, "run-abc")
	if !strings.Contains(script, "trap") || !strings.Contains(script, "docker stop") {
		t.Fatalf("expected trap+docker stop, got %s", script)
	}
	if !strings.Contains(script, "'docker'") || !strings.Contains(script, "'run'") {
		t.Fatalf("expected docker run, got %s", script)
	}
}

func TestBuildRemoteDockerScriptPassesSecretsByName(t *testing.T) {
	script := buildRemoteDockerScript(store.Job{
		DockerImage: "alpine:3.20",
		EnvSecrets:  map[string]string{"TOKEN": "s3cret"},
	}, "run-abc")
	if !strings.Contains(script, "'-e' 'TOKEN'") {
		t.Fatalf("ssh docker must pass the variable by name, got %s", script)
	}
	if strings.Contains(script, "s3cret") {
		t.Fatalf("secret value must not be in the remote argv: %s", script)
	}
}
