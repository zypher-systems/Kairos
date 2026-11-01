package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// applyTransferTarget derives where a transfer job runs from its endpoints:
// on the agent that owns an agent endpoint or an agent storage location,
// otherwise on the server. The client does not choose; the endpoints do.
// A route with an agent on it cannot use a directory on the server: the
// agent would open that path on its own disk.
func (s *Store) applyTransferTarget(ctx context.Context, j *Job) error {
	if j.ExecMode != "transfer" {
		return nil
	}
	var agent *uuid.UUID
	var onServer string // a side only the server can reach
	for i, e := range []TransferEndpoint{j.TransferConfig.Source, j.TransferConfig.Dest} {
		var id *uuid.UUID
		switch e.Kind {
		case "agent":
			id = e.AgentID
		case "location":
			if e.LocationID == nil {
				continue
			}
			var kind, name, agentID string
			err := s.pool.QueryRow(ctx, `
				SELECT kind, name, COALESCE(config->>'agent_id', '') FROM storage_locations WHERE id = $1`,
				*e.LocationID).Scan(&kind, &name, &agentID)
			if err != nil {
				return fmt.Errorf("storage location %s not found", e.LocationID)
			}
			if kind == "agent" {
				aid, err := uuid.Parse(agentID)
				if err != nil {
					return errors.New("agent storage location has no agent")
				}
				id = &aid
			}
			// A trigger-driven transfer reads from the watched place, so
			// its source endpoint is not a side it opens.
			fromTriggerSource := i == 0 && j.TransferConfig.FromTrigger
			if kind == "mount" && !fromTriggerSource {
				onServer = name
			}
		}
		if id == nil {
			continue
		}
		if agent != nil && *agent != *id {
			return errors.New("a transfer cannot span two different agents")
		}
		agent = id
	}
	// A trigger-driven transfer reads from the watched place; an agent
	// watch means the agent must run it.
	if j.TransferConfig.FromTrigger {
		if !j.WatchEnabled {
			// The workflow's trigger counts when the job has none, and then
			// the workflow's watched location is the source.
			var wfWatch bool
			var kind, name string
			if j.WorkflowID != nil {
				_ = s.pool.QueryRow(ctx, `
					SELECT w.watch_enabled, COALESCE(l.kind, ''), COALESCE(l.name, '')
					FROM workflows w LEFT JOIN storage_locations l ON l.id = w.watch_location_id
					WHERE w.id = $1`, *j.WorkflowID).Scan(&wfWatch, &kind, &name)
			}
			if !wfWatch {
				return errors.New("\"use the files from the trigger\" needs a file trigger on the job or on its workflow")
			}
			if kind == "mount" {
				onServer = name
			}
		}
		var wid *uuid.UUID
		switch j.WatchTarget {
		case "agent":
			wid = j.WatchAgentID
		case "location":
			if j.WatchLocationID != nil {
				var kind, name, agentID string
				if err := s.pool.QueryRow(ctx, `
					SELECT kind, name, COALESCE(config->>'agent_id', '') FROM storage_locations WHERE id = $1`,
					*j.WatchLocationID).Scan(&kind, &name, &agentID); err != nil {
					return fmt.Errorf("watched storage location %s not found", j.WatchLocationID)
				}
				if kind == "agent" {
					if aid, err := uuid.Parse(agentID); err == nil {
						wid = &aid
					}
				}
				if kind == "mount" {
					onServer = name
				}
			}
		case "local", "":
			if j.WatchEnabled {
				onServer = "the server filesystem"
			}
		}
		if wid != nil {
			if agent != nil && *agent != *wid {
				return errors.New("the trigger watches one agent and the transfer targets another")
			}
			agent = wid
		}
	}
	if agent != nil {
		if onServer != "" {
			return fmt.Errorf("%s is on the server and this transfer runs on an agent, which cannot reach it: use an agent directory, or a location the agent can connect to", onServer)
		}
		j.TargetType = "agent"
		sel, _ := json.Marshal(map[string]string{"agent_id": agent.String()})
		j.TargetSelector = sel
		return nil
	}
	j.TargetType = "local"
	j.TargetSelector = json.RawMessage(`{}`)
	return nil
}
