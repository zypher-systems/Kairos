// Package endpoints turns the places a job refers to (storage locations,
// SSH hosts, agents, the library) into storage specs a worker can open,
// and decides which worker runs a transfer.
package endpoints

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/storage"
)

// Resolved is one transfer side, ready to open.
type Resolved struct {
	// Library is set for the managed library; Spec is then unused.
	Library bool
	// AgentID is set when the side lives on an agent machine: the agent
	// opens Spec (a local root) itself.
	AgentID *uuid.UUID
	Spec    storage.Spec
	// Path is the file, glob or directory inside the backend.
	Path string
	// Name describes the side for logs.
	Name string
	// SSHHostID is set for SFTP sides so a learned host key can be pinned.
	SSHHostID *uuid.UUID
}

// Resolve builds the storage spec for an endpoint. Secrets are revealed;
// the result must not be logged.
func Resolve(ctx context.Context, st *store.Store, e store.TransferEndpoint) (Resolved, error) {
	switch e.Kind {
	case "library":
		return Resolved{Library: true, Path: e.Path, Name: "library"}, nil
	case "location":
		if e.LocationID == nil {
			return Resolved{}, errors.New("location endpoint has no location id")
		}
		loc, err := st.GetStorageLocationForDelivery(ctx, *e.LocationID)
		if err != nil {
			return Resolved{}, fmt.Errorf("storage location %s: not found (was it deleted?)", e.LocationID)
		}
		r, err := FromLocation(ctx, st, loc)
		if err != nil {
			return Resolved{}, err
		}
		r.Path = e.Path
		return r, nil
	case "ssh":
		if e.SSHHostID == nil {
			return Resolved{}, errors.New("ssh endpoint has no host id")
		}
		spec, name, err := sftpSpec(ctx, st, *e.SSHHostID, "")
		if err != nil {
			return Resolved{}, err
		}
		id := *e.SSHHostID
		return Resolved{Spec: spec, Path: e.Path, Name: name, SSHHostID: &id}, nil
	case "agent":
		if e.AgentID == nil {
			return Resolved{}, errors.New("agent endpoint has no agent id")
		}
		id := *e.AgentID
		ag, err := st.GetAgentByID(ctx, id)
		if err != nil {
			return Resolved{}, fmt.Errorf("agent %s: not found", id)
		}
		return Resolved{AgentID: &id, Spec: storage.Spec{Kind: storage.KindLocal}, Path: e.Path, Name: "agent " + ag.Name}, nil
	}
	return Resolved{}, fmt.Errorf("unknown endpoint kind %q", e.Kind)
}

// FromLocation builds the spec for a storage location (secrets revealed).
func FromLocation(ctx context.Context, st *store.Store, loc store.StorageLocation) (Resolved, error) {
	switch loc.Kind {
	case "mount":
		return Resolved{Spec: storage.Spec{Kind: storage.KindLocal, Root: loc.BasePath}, Name: loc.Name}, nil
	case "sftp":
		if loc.SSHHostID == nil {
			return Resolved{}, fmt.Errorf("storage location %s has no SSH host", loc.Name)
		}
		spec, _, err := sftpSpec(ctx, st, *loc.SSHHostID, loc.BasePath)
		if err != nil {
			return Resolved{}, err
		}
		id := *loc.SSHHostID
		return Resolved{Spec: spec, Name: loc.Name, SSHHostID: &id}, nil
	case "nfs":
		return Resolved{Spec: storage.Spec{
			Kind: storage.KindNFS, Root: loc.BasePath,
			Host: loc.Host, Export: loc.Export, UID: loc.UID, GID: loc.GID,
		}, Name: loc.Name}, nil
	case "smb":
		return Resolved{Spec: storage.Spec{
			Kind: storage.KindSMB, Root: loc.BasePath,
			Host: loc.Host, Bucket: loc.Share, Domain: loc.Domain,
			Username: loc.Username, Password: loc.SecretKey,
		}, Name: loc.Name}, nil
	case "s3":
		return Resolved{Spec: storage.Spec{
			Kind: storage.KindS3, Root: loc.BasePath,
			Endpoint: loc.Endpoint, Region: loc.Region, Bucket: loc.Bucket,
			AccessKey: loc.AccessKey, SecretKey: loc.SecretKey,
			PathStyle: loc.PathStyle, Insecure: loc.Insecure,
		}, Name: loc.Name}, nil
	case "agent":
		if loc.AgentID == nil {
			return Resolved{}, fmt.Errorf("storage location %s has no agent", loc.Name)
		}
		id := *loc.AgentID
		return Resolved{AgentID: &id, Spec: storage.Spec{Kind: storage.KindLocal, Root: loc.BasePath}, Name: loc.Name}, nil
	}
	return Resolved{}, fmt.Errorf("storage location %s has unknown kind %q", loc.Name, loc.Kind)
}

func sftpSpec(ctx context.Context, st *store.Store, hostID uuid.UUID, root string) (storage.Spec, string, error) {
	key, password, fp, ok, err := st.GetSSHCredentials(ctx, hostID)
	if err != nil {
		return storage.Spec{}, "", fmt.Errorf("decrypt host credentials: %w", err)
	}
	if !ok {
		return storage.Spec{}, "", errors.New("ssh host not found")
	}
	host, err := st.GetSSHHost(ctx, hostID)
	if err != nil {
		return storage.Spec{}, "", fmt.Errorf("load ssh host: %w", err)
	}
	return storage.Spec{
		Kind: storage.KindSFTP, Root: root,
		Host: host.Host, Port: host.Port, Username: host.Username,
		PrivateKeyPEM: key, Password: password,
		HostKeyFingerprint: fp, VerifyHostKey: host.VerifyHostKey,
	}, host.Name, nil
}

// TransferAgent returns the agent that must execute a transfer, or nil
// when the server does. A transfer with an agent on either side runs on
// that agent; two different agents cannot be combined.
func TransferAgent(ctx context.Context, st *store.Store, c store.TransferConfig) (*uuid.UUID, error) {
	var agent *uuid.UUID
	for _, e := range []store.TransferEndpoint{c.Source, c.Dest} {
		var id *uuid.UUID
		switch e.Kind {
		case "agent":
			id = e.AgentID
		case "location":
			if e.LocationID == nil {
				continue
			}
			loc, err := st.GetStorageLocation(ctx, *e.LocationID)
			if err != nil {
				return nil, fmt.Errorf("storage location %s not found", e.LocationID)
			}
			if loc.Kind == "agent" {
				id = loc.AgentID
			}
		}
		if id == nil {
			continue
		}
		if agent != nil && *agent != *id {
			return nil, errors.New("a transfer cannot span two different agents")
		}
		agent = id
	}
	return agent, nil
}

// WatchSide resolves what a job's file trigger watches, so a
// trigger-driven transfer reads from the same place.
func WatchSide(ctx context.Context, st *store.Store, job store.Job) (Resolved, error) {
	// A job without its own trigger inside a workflow that has one reads
	// from the workflow's watched location.
	if !job.WatchEnabled && job.WorkflowID != nil {
		wf, err := st.GetWorkflow(ctx, *job.WorkflowID)
		if err == nil && wf.WatchEnabled && wf.WatchLocationID != nil {
			return Resolve(ctx, st, store.TransferEndpoint{Kind: "location", LocationID: wf.WatchLocationID})
		}
		return Resolved{}, errors.New("neither the job nor its workflow has a file trigger")
	}
	switch job.WatchTarget {
	case "location":
		if job.WatchLocationID == nil {
			return Resolved{}, errors.New("file trigger has no location")
		}
		return Resolve(ctx, st, store.TransferEndpoint{Kind: "location", LocationID: job.WatchLocationID})
	case "ssh":
		return Resolve(ctx, st, store.TransferEndpoint{Kind: "ssh", SSHHostID: job.WatchSSHHostID})
	case "agent":
		return Resolve(ctx, st, store.TransferEndpoint{Kind: "agent", AgentID: job.WatchAgentID})
	case "local", "":
		return Resolved{Spec: storage.Spec{Kind: storage.KindLocal}, Name: "server filesystem"}, nil
	}
	return Resolved{}, fmt.Errorf("unknown watch target %q", job.WatchTarget)
}
