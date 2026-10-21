package secrets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/vaultkv"
)

// HTTPClient is replaced in tests.
var HTTPClient *http.Client

// Resolve fetches store-backed refs into job.EnvSecrets. A failed fetch
// returns an error so the claim fails the run with that message.
func Resolve(ctx context.Context, st *store.Store, job *store.Job) error {
	if job == nil || len(job.EnvSecretRefs) == 0 {
		return nil
	}
	if job.EnvSecrets == nil {
		job.EnvSecrets = map[string]string{}
	}
	for _, ref := range job.EnvSecretRefs {
		if ref.Name == "" || ref.StoreID == uuid.Nil || ref.Path == "" || ref.Key == "" {
			return fmt.Errorf("secret store: incomplete ref for %s", nonempty(ref.Name, "env"))
		}
		ss, err := st.GetSecretStoreForDelivery(ctx, ref.StoreID)
		if err != nil {
			return fmt.Errorf("secret store: could not resolve %s", ref.Name)
		}
		val, err := vaultkv.Get(ctx, HTTPClient, vaultkv.Request{
			Address:   ss.Address,
			Token:     ss.Token,
			Mount:     ss.Mount,
			Namespace: ss.Namespace,
			Path:      ref.Path,
			Key:       ref.Key,
		})
		if err != nil {
			return fmt.Errorf("secret store: could not resolve %s", ref.Name)
		}
		job.EnvSecrets[ref.Name] = val
	}
	return nil
}

func nonempty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
