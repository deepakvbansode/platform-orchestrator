package state

import (
	"context"
	"time"
)

// DeployMeta is stored in deploy_meta.json alongside state.yaml.
type DeployMeta struct {
	LastDeployedAt   time.Time `json:"last_deployed_at"`
	LastDeployStatus string    `json:"last_deploy_status"` // "success" | "deploy_failed" | "generate_failed"
	DeployersRun     []string  `json:"deployers_run"`
}

// WorkloadFiles holds the files needed by the status endpoint.
type WorkloadFiles struct {
	ScoreYAML  []byte      // content of score.yaml; nil if not found
	StateYAML  []byte      // content of state.yaml; nil if not found
	DeployMeta *DeployMeta // nil if deploy_meta.json not found
}

// StateConflictError is returned by PushState when the ETag has changed since pull.
type StateConflictError struct{}

func (e *StateConflictError) Error() string {
	return "state was modified by a concurrent deployment, retry"
}

// StateBackend abstracts storage of per-workload state files.
// State is namespaced as {org}/{env}/{workload}/.
type StateBackend interface {
	// PullState reads state.yaml and returns its content + ETag.
	// Returns nil stateYAML and empty etag when the workload has no stored state.
	PullState(ctx context.Context, org, env, workload string) (stateYAML []byte, etag string, err error)

	// PushState writes state.yaml with optimistic concurrency: if the stored ETag
	// no longer matches etag, returns *StateConflictError.
	// Pass etag="" to skip the check (fresh workload).
	PushState(ctx context.Context, org, env, workload string, stateYAML []byte, etag string) error

	// PushMeta writes deploy_meta.json only (last-write-wins). Used on generate failure.
	PushMeta(ctx context.Context, org, env, workload string, meta *DeployMeta) error

	// PushArtifacts writes score.yaml, manifests.yaml, and deploy_meta.json (last-write-wins).
	PushArtifacts(ctx context.Context, org, env, workload string, scoreYAML, manifestsYAML []byte, meta *DeployMeta) error

	// GetStatus reads score.yaml, state.yaml, and deploy_meta.json for the status endpoint.
	// Returns a WorkloadFiles where nil fields mean the file was not found.
	GetStatus(ctx context.Context, org, env, workload string) (*WorkloadFiles, error)

	// GetManifest reads manifests.yaml. Returns nil, nil when not found.
	GetManifest(ctx context.Context, org, env, workload string) ([]byte, error)
}
