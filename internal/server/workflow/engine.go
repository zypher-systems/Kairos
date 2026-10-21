// Package workflow starts DAG executions by hand. Fan-out on completion
// lives in store.CompleteClaim so it commits with the run's result.
package workflow

import (
	"context"
	"time"

	"github.com/zypher-systems/kairos/internal/server/store"
	"github.com/zypher-systems/kairos/internal/server/ws"
)

// RunWorkflow manually triggers a workflow by enqueueing its root jobs
// (those with no incoming edges) under a single execution id.
func RunWorkflow(ctx context.Context, st *store.Store, hub *ws.Hub, wf store.Workflow) (int, error) {
	_, runs, err := st.StartWorkflow(ctx, wf, store.TriggerManual, time.Now(), nil)
	if err != nil {
		return 0, err
	}
	for _, run := range runs {
		if hub != nil {
			hub.Publish(ws.TopicActivity, "run_queued", map[string]any{
				"id":          run.ID,
				"job_id":      run.JobID,
				"status":      run.Status,
				"target_type": run.TargetType,
				"trigger":     store.TriggerManual,
			})
		}
	}
	return len(runs), nil
}
