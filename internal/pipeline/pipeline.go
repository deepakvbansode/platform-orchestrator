package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/score-spec/score-orchestrator/internal/deployer"
	oerr "github.com/score-spec/score-orchestrator/internal/errors"
	"github.com/score-spec/score-orchestrator/internal/provisioner"
	"github.com/score-spec/score-orchestrator/internal/score"
	"github.com/score-spec/score-orchestrator/internal/state"
)

// Runner holds the wired-up dependencies for the deploy pipeline.
type Runner struct {
	Backend     state.StateBackend
	Provisioner provisioner.ProvisionerSource
	Deployers   []deployer.Deployer
}

// Run executes the 12-step deploy pipeline for the given request.
// Returns a DeployResult on success, or an *oerr.OrchestratorError on failure.
// When a deployer fails, both result and error are returned (state was still pushed).
func (r *Runner) Run(ctx context.Context, req DeployRequest) (*DeployResult, *oerr.OrchestratorError) {
	// Step 1: Validate input.
	if oe := ValidateInput(req.Org, req.Env, req.Workload, req.ScoreYAML); oe != nil {
		return nil, oe
	}

	// Step 2: Pull state.yaml from backend.
	stateYAML, etag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to pull state from backend", err.Error(), "state", 500)
	}
	isFresh := stateYAML == nil

	// Step 3: Sync provisioners into a temp dir.
	provTmp, err := os.MkdirTemp("", "score-provisioners-*")
	if err != nil {
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to create provisioner temp dir", err.Error(), "provisioner", 500)
	}
	defer os.RemoveAll(provTmp)

	if syncErr := r.Provisioner.Sync(ctx, provTmp); syncErr != nil {
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to sync provisioners", syncErr.Error(), "provisioner", 500)
	}

	// Step 4: Create temp working directory.
	workDir, err := os.MkdirTemp("", "score-workdir-*")
	if err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to create work dir", err.Error(), "generate", 500)
	}
	defer os.RemoveAll(workDir)

	scoreDotK8s := filepath.Join(workDir, ".score-k8s")
	if err := os.MkdirAll(scoreDotK8s, 0755); err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to create .score-k8s dir", err.Error(), "generate", 500)
	}

	// Write score.yaml into work dir.
	if err := os.WriteFile(filepath.Join(workDir, "score.yaml"), req.ScoreYAML, 0644); err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to write score.yaml", err.Error(), "generate", 500)
	}

	// Write state.yaml (skip for fresh workload).
	if !isFresh {
		if err := os.WriteFile(filepath.Join(scoreDotK8s, "state.yaml"), stateYAML, 0644); err != nil {
			return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to write state.yaml", err.Error(), "generate", 500)
		}
	}

	// Copy provisioner files into .score-k8s/.
	provFiles, _ := filepath.Glob(filepath.Join(provTmp, "*.provisioners.yaml"))
	for _, pf := range provFiles {
		data, err := os.ReadFile(pf)
		if err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to read provisioner file", err.Error(), "provisioner", 500)
		}
		if err := os.WriteFile(filepath.Join(scoreDotK8s, filepath.Base(pf)), data, 0644); err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to write provisioner file", err.Error(), "provisioner", 500)
		}
	}

	// Step 5: Run score-k8s init (fresh workloads only).
	if isFresh {
		if err := score.RunInit(ctx, workDir); err != nil {
			return nil, oerr.New(oerr.CodeScoreGenerateFailed, "score-k8s init failed", err.Error(), "generate", 500)
		}
	}

	// Step 6: Run score-k8s generate.
	manifestsFile := filepath.Join(workDir, "manifests.yaml")
	genOutput, genErr := score.RunGenerate(ctx, workDir, manifestsFile)
	if genErr != nil {
		meta := &state.DeployMeta{
			LastDeployedAt:   time.Now().UTC(),
			LastDeployStatus: "generate_failed",
			DeployersRun:     []string{},
		}
		_ = r.Backend.PushMeta(ctx, req.Org, req.Env, req.Workload, meta)
		return nil, oerr.New(oerr.CodeScoreGenerateFailed,
			"score-k8s generate failed",
			fmt.Sprintf("%s\n%s", genErr.Error(), genOutput),
			"generate", 500)
	}

	// Step 7: Re-read current state ETag and check for conflict.
	// ETag check is done after generate (which is purely local) to avoid an extra
	// network round-trip before the fast local generate step.
	_, currentETag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to re-read state for ETag check", err.Error(), "state", 500)
	}
	if !isFresh && currentETag != etag {
		return nil, oerr.New(oerr.CodeStateConflict, "state was modified by a concurrent deployment, retry", "", "state", 409)
	}

	// Read updated state.yaml produced by score-k8s generate.
	updatedStateYAML, err := os.ReadFile(filepath.Join(scoreDotK8s, "state.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to read updated state.yaml", err.Error(), "generate", 500)
	}
	// For existing workloads, score-k8s generate must always produce state.yaml.
	// If it is absent, the ETag concurrency guard cannot be applied safely.
	if updatedStateYAML == nil && !isFresh {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "score-k8s generate did not produce state.yaml for existing workload", "", "generate", 500)
	}

	// Read generated manifests.
	manifestsYAML, err := os.ReadFile(manifestsFile)
	if err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to read manifests.yaml", err.Error(), "generate", 500)
	}

	// Step 8: Run deployers in sequence.
	deployReq := deployer.DeployRequest{
		Org:           req.Org,
		Env:           req.Env,
		Workload:      req.Workload,
		ManifestsPath: manifestsFile,
	}
	var deployErr error
	var deployersRun []string
	for _, d := range r.Deployers {
		deployersRun = append(deployersRun, d.Name())
		if err := d.Deploy(ctx, deployReq); err != nil {
			deployErr = fmt.Errorf("deployer %q failed: %w", d.Name(), err)
			break
		}
	}

	deployStatus := "success"
	if deployErr != nil {
		deployStatus = "deploy_failed"
	}

	// Step 9: Push state.yaml with ETag precondition.
	if updatedStateYAML != nil {
		if err := r.Backend.PushState(ctx, req.Org, req.Env, req.Workload, updatedStateYAML, etag); err != nil {
			if _, ok := err.(*state.StateConflictError); ok {
				return nil, oerr.New(oerr.CodeStateConflict, err.Error(), "", "state", 409)
			}
			return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push state.yaml", err.Error(), "state", 500)
		}
	}

	// Step 10: Push artifacts (last-write-wins).
	meta := &state.DeployMeta{
		LastDeployedAt:   time.Now().UTC(),
		LastDeployStatus: deployStatus,
		DeployersRun:     deployersRun,
	}
	if err := r.Backend.PushArtifacts(ctx, req.Org, req.Env, req.Workload, req.ScoreYAML, manifestsYAML, meta); err != nil {
		return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push artifacts", err.Error(), "state", 500)
	}

	// Steps 11–12: workDir and provTmp cleanup deferred above; return result.
	result := &DeployResult{
		Org:          req.Org,
		Env:          req.Env,
		Workload:     req.Workload,
		Status:       deployStatus,
		DeployersRun: deployersRun,
		DeployedAt:   meta.LastDeployedAt,
	}
	if deployErr != nil {
		return result, oerr.New(oerr.CodeDeployFailed, deployErr.Error(), "", "deploy", 500)
	}
	return result, nil
}
