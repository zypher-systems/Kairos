package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// TransferEndpoint is one side of a file transfer. Kinds:
//
//   - location: a storage location (mount, sftp, s3, agent) by id; Path is
//     a file or glob relative to the location's base path, or a directory
//     (trailing slash) on the destination side.
//   - library:  the managed file library on the Kairos server.
//   - ssh, agent: the pre-1.16 forms, an absolute path on an SSH host or an
//     agent machine. Still accepted; new jobs use locations.
type TransferEndpoint struct {
	Kind       string     `json:"kind"` // location | library | ssh | agent
	LocationID *uuid.UUID `json:"location_id,omitempty"`
	FileID     *uuid.UUID `json:"file_id,omitempty"`
	SSHHostID  *uuid.UUID `json:"ssh_host_id,omitempty"`
	AgentID    *uuid.UUID `json:"agent_id,omitempty"`
	Path       string     `json:"path"` // path or glob (source), path or directory (dest), filename (library dest)
}

// TransferConfig describes a file move.
type TransferConfig struct {
	Source       TransferEndpoint `json:"source"`
	Dest         TransferEndpoint `json:"dest"`
	Overwrite    bool             `json:"overwrite"`
	DeleteSource bool             `json:"delete_source"`
	// PreserveMTime keeps the source's modification time on the copy.
	PreserveMTime bool `json:"preserve_mtime,omitempty"`
	// VerifyChecksum re-reads the destination and compares SHA-256.
	VerifyChecksum bool `json:"verify_checksum,omitempty"`
	// FromTrigger makes the source the files the job's file trigger matched
	// when it fired: the source endpoint must be the watched location (or
	// legacy target) and Source.Path is ignored.
	FromTrigger bool `json:"from_trigger,omitempty"`
}

// Validate checks transfer invariants.
func (c *TransferConfig) Validate() error {
	if err := c.Source.validate("source", c.FromTrigger); err != nil {
		return err
	}
	if err := c.Dest.validate("dest", false); err != nil {
		return err
	}
	if c.Source.Kind == "library" && c.Dest.Kind == "library" {
		return errors.New("at least one endpoint must be outside the library")
	}
	if c.Source.Kind == "location" && c.Dest.Kind == "location" &&
		c.Source.LocationID != nil && c.Dest.LocationID != nil &&
		*c.Source.LocationID == *c.Dest.LocationID && c.Source.Path == c.Dest.Path {
		return errors.New("source and destination are identical")
	}
	if c.FromTrigger && c.Source.Kind == "library" {
		return errors.New("a trigger-driven transfer reads from the watched location, not the library")
	}
	if c.Source.Kind == c.Dest.Kind && c.Source.Kind == "ssh" &&
		c.Source.SSHHostID == c.Dest.SSHHostID && c.Source.Path == c.Dest.Path {
		return errors.New("source and destination are identical")
	}
	if c.Source.Kind == c.Dest.Kind && c.Source.Kind == "agent" &&
		c.Source.AgentID == c.Dest.AgentID && c.Source.Path == c.Dest.Path {
		return errors.New("source and destination are identical")
	}
	// Agent endpoints are executed by the agent over the agent channel; both
	// ends on agents is not supported (two machines would have to relay).
	if c.Source.Kind == "agent" && c.Dest.Kind == "agent" {
		return errors.New("agent-to-agent is not supported; one endpoint must be the library")
	}
	// Agent transfers are claimed and executed by the agent, so the job's
	// run target must be that agent.
	return nil
}

func (e *TransferEndpoint) validate(side string, fromTrigger bool) error {
	needPath := side == "source" && !fromTrigger
	switch e.Kind {
	case "location":
		if e.LocationID == nil {
			return errors.New(side + ".location_id is required for location endpoints")
		}
		if needPath && e.Path == "" {
			return errors.New(side + ".path is required: a file or a glob inside the location")
		}
	case "library":
		if side == "source" && e.FileID == nil {
			return errors.New("source.file_id is required for library sources")
		}
	case "ssh":
		if e.SSHHostID == nil {
			return errors.New(side + ".ssh_host_id is required for ssh endpoints")
		}
		if (needPath || side == "dest") && e.Path == "" {
			return errors.New(side + ".path is required for ssh endpoints")
		}
	case "agent":
		if e.AgentID == nil {
			return errors.New(side + ".agent_id is required for agent endpoints")
		}
		if (needPath || side == "dest") && e.Path == "" {
			return errors.New(side + ".path is required for agent endpoints")
		}
	default:
		return errors.New(side + ".kind must be location, library, ssh, or agent")
	}
	return nil
}

// LocationIDs lists the storage locations the transfer refers to.
func (c *TransferConfig) LocationIDs() []uuid.UUID {
	var out []uuid.UUID
	for _, e := range []TransferEndpoint{c.Source, c.Dest} {
		if e.Kind == "location" && e.LocationID != nil {
			out = append(out, *e.LocationID)
		}
	}
	return out
}

// WatchJob is the lightweight per-tick view of a file-trigger job used by
// the watcher loop (full job loaded only when firing).
type WatchJob struct {
	ID              uuid.UUID  `json:"id"`
	WatchTarget     string     `json:"watch_target"`
	WatchSSHHostID  *uuid.UUID `json:"watch_ssh_host_id,omitempty"`
	WatchLocationID *uuid.UUID `json:"watch_location_id,omitempty"`
	WatchPath       string     `json:"watch_path"`
	WatchInterval   int        `json:"watch_interval_seconds"`
	LastSig         string     `json:"watch_last_sig"`
}

// ListWatchJobs returns enabled jobs with the file trigger armed.
func (s *Store) ListWatchJobs(ctx context.Context) ([]WatchJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, watch_target, watch_ssh_host_id, watch_location_id, watch_path, watch_interval_seconds, watch_last_sig
		FROM jobs WHERE enabled AND watch_enabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchJob
	for rows.Next() {
		var w WatchJob
		if err := rows.Scan(&w.ID, &w.WatchTarget, &w.WatchSSHHostID, &w.WatchLocationID, &w.WatchPath, &w.WatchInterval, &w.LastSig); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SetRunTriggerFiles records the files a file trigger matched when it
// queued this run.
func (s *Store) SetRunTriggerFiles(ctx context.Context, runID uuid.UUID, files []string) error {
	if files == nil {
		files = []string{}
	}
	raw, err := json.Marshal(files)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE runs SET trigger_files = $2 WHERE id = $1`, runID, raw)
	return err
}

// SetWatchSig persists the last-fired watch signature for a job.
func (s *Store) SetWatchSig(ctx context.Context, id uuid.UUID, sig string) error {
	_, err := s.pool.Exec(ctx, `UPDATE jobs SET watch_last_sig = $2 WHERE id = $1`, id, sig)
	return err
}
