package store

import "errors"

// Sentinel errors returned by fenced queue and log operations.
var (
	// ErrStaleClaim means the caller no longer owns the run (wrong agent,
	// superseded epoch after a lease requeue, or the claim is gone).
	ErrStaleClaim = errors.New("stale or foreign claim")
	// ErrLogCapped means the run has hit the per-run log volume ceiling.
	ErrLogCapped = errors.New("run log volume capped")
	// ErrDAGCycle means the submitted workflow edges contain a cycle.
	ErrDAGCycle = errors.New("workflow edges form a cycle")
	// ErrAccountDisabled is returned when a matching username exists but is off.
	ErrAccountDisabled = errors.New("account disabled")
	// ErrLastAdmin refuses demote/disable/delete of the last enabled admin.
	ErrLastAdmin = errors.New("cannot modify the last admin")
	// ErrUserNotFound is a missing account.
	ErrUserNotFound = errors.New("user not found")
	// ErrUsernameTaken is a unique-username collision.
	ErrUsernameTaken = errors.New("username already exists")
	// ErrAmbiguousName means more than one row shares the lookup name.
	ErrAmbiguousName = errors.New("multiple resources share this name")
	// ErrOIDCSubjectTaken is a unique oidc_subject collision.
	ErrOIDCSubjectTaken = errors.New("OIDC subject already linked to another user")
	// ErrOIDCUnlinked means the IdP subject has no Kairos account.
	ErrOIDCUnlinked = errors.New("no Kairos account is linked to this identity")
)
