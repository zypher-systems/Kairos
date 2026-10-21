package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func ambiguous(kind, name string) error {
	return fmt.Errorf("%w: %s %q", ErrAmbiguousName, kind, name)
}

// ListAllJobs returns every job, name-sorted, uncapped.
func (s *Store) ListAllJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs ORDER BY name ASC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// JobByName returns the unique job with this name.
func (s *Store) JobByName(ctx context.Context, name string) (Job, error) {
	jobs, err := s.jobsByName(ctx, name)
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, pgx.ErrNoRows
	}
	if len(jobs) > 1 {
		return Job{}, ambiguous("job", name)
	}
	return jobs[0], nil
}

func (s *Store) jobsByName(ctx context.Context, name string) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM jobs WHERE name = $1 ORDER BY created_at`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// WorkflowByName returns the unique workflow with this name.
func (s *Store) WorkflowByName(ctx context.Context, name string) (Workflow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+workflowCols+` FROM workflows WHERE name = $1 ORDER BY created_at`, name)
	if err != nil {
		return Workflow{}, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return Workflow{}, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return Workflow{}, err
	}
	if len(out) == 0 {
		return Workflow{}, pgx.ErrNoRows
	}
	if len(out) > 1 {
		return Workflow{}, ambiguous("workflow", name)
	}
	return out[0], nil
}

// SSHHostByName returns the unique SSH host with this name.
func (s *Store) SSHHostByName(ctx context.Context, name string) (SSHHost, error) {
	hosts, err := s.ListSSHHosts(ctx)
	if err != nil {
		return SSHHost{}, err
	}
	var match []SSHHost
	for _, h := range hosts {
		if h.Name == name {
			match = append(match, h)
		}
	}
	if len(match) == 0 {
		return SSHHost{}, pgx.ErrNoRows
	}
	if len(match) > 1 {
		return SSHHost{}, ambiguous("ssh host", name)
	}
	return match[0], nil
}

// AgentByName returns the unique agent with this name.
func (s *Store) AgentByName(ctx context.Context, name string) (Agent, error) {
	agents, err := s.ListAgents(ctx)
	if err != nil {
		return Agent{}, err
	}
	var match []Agent
	for _, a := range agents {
		if a.Name == name {
			match = append(match, a)
		}
	}
	if len(match) == 0 {
		return Agent{}, pgx.ErrNoRows
	}
	if len(match) > 1 {
		return Agent{}, ambiguous("agent", name)
	}
	return match[0], nil
}

// ChannelByName returns the unique notification channel with this name.
func (s *Store) ChannelByName(ctx context.Context, name string) (NotificationChannel, error) {
	chs, err := s.ListChannels(ctx)
	if err != nil {
		return NotificationChannel{}, err
	}
	var match []NotificationChannel
	for _, c := range chs {
		if c.Name == name {
			match = append(match, c)
		}
	}
	if len(match) == 0 {
		return NotificationChannel{}, pgx.ErrNoRows
	}
	if len(match) > 1 {
		return NotificationChannel{}, ambiguous("channel", name)
	}
	return match[0], nil
}

// FileByName returns the unique library file with this name.
func (s *Store) FileByName(ctx context.Context, name string) (FileEntry, error) {
	files, err := s.ListFiles(ctx)
	if err != nil {
		return FileEntry{}, err
	}
	var match []FileEntry
	for _, f := range files {
		if f.Name == name {
			match = append(match, f)
		}
	}
	if len(match) == 0 {
		return FileEntry{}, pgx.ErrNoRows
	}
	if len(match) > 1 {
		return FileEntry{}, ambiguous("file", name)
	}
	return match[0], nil
}

// SSHHostName resolves a host id to its name (empty if missing).
func (s *Store) SSHHostName(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	h, err := s.GetSSHHost(ctx, *id)
	if err != nil {
		return ""
	}
	return h.Name
}

// AgentName resolves an agent id to its name.
func (s *Store) AgentName(ctx context.Context, id uuid.UUID) string {
	a, err := s.GetAgentByID(ctx, id)
	if err != nil {
		return ""
	}
	return a.Name
}

// FileName resolves a library file id to its name.
func (s *Store) FileName(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	f, err := s.GetFile(ctx, *id)
	if err != nil {
		return ""
	}
	return f.Name
}

// ChannelName resolves a channel id to its name.
func (s *Store) ChannelName(ctx context.Context, id uuid.UUID) string {
	c, err := s.GetChannel(ctx, id)
	if err != nil {
		return ""
	}
	return c.Name
}

// MissingName is a resolved-reference error.
func MissingName(kind, name string) error {
	return fmt.Errorf("%s %q not found", kind, name)
}

// IsNoRows reports pgx.ErrNoRows.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
