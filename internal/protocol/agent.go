// Package protocol defines wire types shared between kairosd and kairos-agent.
package protocol

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/storage"
)

// AgentRegisterRequest is sent once at agent startup. Labels are set on the
// server when the agent is created and cannot be changed from the agent;
// any labels sent here are only logged when they differ.
type AgentRegisterRequest struct {
	Name         string            `json:"name"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capabilities map[string]string `json:"capabilities"`
}

// AgentInfo is the server's view of an agent.
type AgentInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Running int    `json:"running"`
}

// ClaimRequest asks the server for the next run for this agent.
type ClaimRequest struct {
	AgentID string `json:"agent_id"`
}

// RunSpec is the execution payload delivered to an agent when it claims work.
type RunSpec struct {
	RunID       uuid.UUID         `json:"run_id"`
	JobID       uuid.UUID         `json:"job_id"`
	JobName     string            `json:"job_name"`
	ExecMode    string            `json:"exec_mode"` // shell | docker | transfer
	DockerImage string            `json:"docker_image"`
	DockerArgs  []string          `json:"docker_args"`
	ShellScript string            `json:"shell_script"`
	Workdir     string            `json:"workdir"`
	Env         map[string]string `json:"env"`
	// SecretKeys names the Env entries that are secrets, so the agent can
	// redact their values from the run log.
	SecretKeys []string `json:"secret_keys,omitempty"`
	TimeoutSec int      `json:"timeout_seconds"`
	ClaimEpoch int      `json:"claim_epoch"`
	// LeaseSeconds is the server's claim lease. The agent renews well within
	// it and kills the run when it cannot renew before it lapses, because
	// the server hands the run to another worker at that point.
	LeaseSeconds int `json:"lease_seconds"`
	// Transfer jobs targeting this agent: the agent downloads the payload
	// from the server (source) or uploads to it (dest) over the agent API.
	Transfer *TransferSpec `json:"transfer,omitempty"`
}

// TransferSpec describes a file move an agent executes. Each side is the
// library (over the agent API) or a storage spec the agent opens itself:
// its own filesystem, an SFTP host or an S3 bucket, credentials included.
type TransferSpec struct {
	Source TransferSide `json:"source"`
	Dest   TransferSide `json:"dest"`
	// Files, when set, are the exact source paths to copy (a file trigger
	// matched them); Source.Path is then ignored.
	Files          []string `json:"files,omitempty"`
	Overwrite      bool     `json:"overwrite"`
	DeleteSource   bool     `json:"delete_source"`
	PreserveMTime  bool     `json:"preserve_mtime,omitempty"`
	VerifyChecksum bool     `json:"verify_checksum,omitempty"`

	// Pre-1.16 fields, still filled for older agents: Direction "push"
	// (library -> agent path) or "pull" (agent path -> library).
	Direction  string `json:"direction,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	FileName   string `json:"file_name,omitempty"`
}

// TransferSide is one end of an agent-executed transfer.
type TransferSide struct {
	// Library is the managed library on the server: FileID names the
	// source file; on the destination side FileName is the name to register.
	Library  bool   `json:"library,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	// Storage is the spec the agent opens; Path the file, glob or directory
	// inside it.
	Storage *storage.Spec `json:"storage,omitempty"`
	Path    string        `json:"path,omitempty"`
	Name    string        `json:"name,omitempty"`
}

// WatchEvent is an agent reporting a matched file arrival on its filesystem.
type WatchEvent struct {
	JobID uuid.UUID `json:"job_id"`
	// Sig is the agent-computed signature of the matched set (name+size+mtime).
	Sig string `json:"sig"`
	// Files are the matched paths, so the run knows what arrived.
	Files []string `json:"files,omitempty"`
}

// WatchTask is a file-watch assignment pushed to an agent.
type WatchTask struct {
	JobID    uuid.UUID `json:"job_id"`
	Glob     string    `json:"glob"`
	Interval int       `json:"interval_seconds"`
	// Root scopes Glob to a directory (an agent storage location); empty
	// means Glob is absolute.
	Root string `json:"root,omitempty"`
}

// ClaimResponse is the payload for a successful claim (one run).
type ClaimResponse struct {
	Run *RunSpec `json:"run,omitempty"`
}

// LogChunk is one ordered piece of output streamed from agent to server.
type LogChunk struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"` // stdout | stderr | system
	Data   string `json:"data"`
}

// LogBatch carries chunks; the server assigns storage ordering by seq.
type LogBatch struct {
	Chunks []LogChunk `json:"chunks"`
}

// LeaseRequest renews a claim. ClaimEpoch fences the renew to the claim
// the agent was handed; 0 is accepted from older agents.
type LeaseRequest struct {
	ClaimEpoch int `json:"claim_epoch"`
}

// StatusReleased is a CompleteRequest status: the agent is shutting down
// and did not finish the run, so the server queues it again.
const StatusReleased = "released"

// CompleteRequest reports the final state of a run.
type CompleteRequest struct {
	Status     string `json:"status"` // succeeded | failed | canceled | released
	ExitCode   *int   `json:"exit_code,omitempty"`
	Error      string `json:"error,omitempty"`
	ClaimEpoch int    `json:"claim_epoch"`
}

// HeartbeatRequest reports liveness and load.
type HeartbeatRequest struct {
	AgentID    string `json:"agent_id"`
	Load       int    `json:"load"` // claims in flight
	LastStatus string `json:"last_status,omitempty"`
}

// HeartbeatResponse may carry server instructions (cancelations, watch tasks).
type HeartbeatResponse struct {
	Now    time.Time   `json:"now"`
	Cancel []string    `json:"cancel,omitempty"` // run ids to kill now
	Watch  []WatchTask `json:"watch,omitempty"`  // file-watch assignments
}

// Marshal is a tiny helper for building JSON bodies.
func Marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
