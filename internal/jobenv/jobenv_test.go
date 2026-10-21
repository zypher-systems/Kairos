package jobenv

import (
	"slices"
	"testing"
)

func TestBaseDropsWorkerSecrets(t *testing.T) {
	env := Base([]string{
		"PATH=/usr/bin",
		"KAIROS_SECRET=s3cret",
		"KAIROS_AGENT_TOKEN=tok",
		"KAIROS_DB_URL=postgres://x",
		"PGPASSWORD=pw",
		"LC_ALL=C.UTF-8",
		"HOME=/home/k",
	})
	want := []string{"PATH=/usr/bin", "LC_ALL=C.UTF-8", "HOME=/home/k"}
	if !slices.Equal(env, want) {
		t.Fatalf("got %v want %v", env, want)
	}
}

func TestMergeReplaces(t *testing.T) {
	got := Merge([]string{"PATH=/usr/bin", "HOME=/h"}, map[string]string{"HOME": "/j", "X": "1"})
	slices.Sort(got)
	want := []string{"HOME=/j", "PATH=/usr/bin", "X=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
