package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestEdgesAcyclicOK(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	edges := []WorkflowEdge{
		{FromJobID: a, ToJobID: b},
		{FromJobID: a, ToJobID: c},
		{FromJobID: b, ToJobID: c},
	}
	if err := EdgesAcyclic(edges); err != nil {
		t.Fatalf("diamond must be acyclic: %v", err)
	}
}

func TestEdgesAcyclicCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	edges := []WorkflowEdge{
		{FromJobID: a, ToJobID: b},
		{FromJobID: b, ToJobID: a},
	}
	if err := EdgesAcyclic(edges); err != ErrDAGCycle {
		t.Fatalf("got %v want ErrDAGCycle", err)
	}
}

func TestEdgesAcyclicEmpty(t *testing.T) {
	if err := EdgesAcyclic(nil); err != nil {
		t.Fatal(err)
	}
}
