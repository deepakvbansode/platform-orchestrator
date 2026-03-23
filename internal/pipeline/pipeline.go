package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/deepakvbansode/platform-orchestrator/internal/deployer"
	oerr "github.com/deepakvbansode/platform-orchestrator/internal/errors"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
	"github.com/deepakvbansode/platform-orchestrator/internal/provisioner"
	"github.com/deepakvbansode/platform-orchestrator/internal/score"
	"github.com/deepakvbansode/platform-orchestrator/internal/state"
)

// Runner holds the wired-up dependencies for the deploy pipeline.
type Runner struct {
	Backend     state.StateBackend
	Provisioner provisioner.ProvisionerSource
	Deployer    deployer.Deployer
}

// Run executes the 12-step deploy pipeline for the given request.
// Returns a DeployResult on success, or an *oerr.OrchestratorError on failure.
// When a deployer fails, both result and error are returned (state was still pushed).
func (r *Runner) Run(ctx context.Context, req DeployRequest) (*DeployResult, *oerr.OrchestratorError) {
	log := logger.Get()
	log.Info("deploy started", "org", req.Org, "env", req.Env, "workload", req.Workload)

	// Step 1: Validate input.
	log.Debug("step 1: validating input")
	if oe := ValidateInput(req.Org, req.Env, req.Workload, req.ScoreYAML); oe != nil {
		log.Error("input validation failed", "code", oe.Code, "message", oe.Message)
		return nil, oe
	}

	// Step 2: Pull state.yaml from backend.
	log.Debug("step 2: pulling state from backend")
	stateYAML, etag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		log.Error("failed to pull state", "error", err)
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to pull state from backend", err.Error(), "state", 500)
	}
	isFresh := stateYAML == nil
	if isFresh {
		log.Info("workload has no existing state — treating as fresh")
	} else {
		log.Debug("existing workload state loaded", "etag", etag)
	}

	// Step 3: Sync provisioners into a temp dir.
	log.Debug("step 3: syncing provisioners")
	provTmp, err := os.MkdirTemp("", "score-provisioners-*")
	if err != nil {
		log.Error("failed to create provisioner temp dir", "error", err)
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to create provisioner temp dir", err.Error(), "provisioner", 500)
	}
	defer os.RemoveAll(provTmp)

	if syncErr := r.Provisioner.Sync(ctx, provTmp); syncErr != nil {
		log.Error("provisioner sync failed", "error", syncErr)
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to sync provisioners", syncErr.Error(), "provisioner", 500)
	}
	log.Debug("provisioners synced", "tmp_dir", provTmp)

	// Step 4: Create temp working directory.
	log.Debug("step 4: preparing work directory")
	workDir, err := os.MkdirTemp("", "score-workdir-*")
	if err != nil {
		log.Error("failed to create work dir", "error", err)
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to create work dir", err.Error(), "generate", 500)
	}
	defer os.RemoveAll(workDir)
	log.Debug("work directory created", "path", workDir)

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
	log.Debug("copying provisioner files into work dir", "count", len(provFiles))
	for _, pf := range provFiles {
		data, err := os.ReadFile(pf)
		if err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to read provisioner file", err.Error(), "provisioner", 500)
		}
		if err := os.WriteFile(filepath.Join(scoreDotK8s, filepath.Base(pf)), data, 0644); err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to write provisioner file", err.Error(), "provisioner", 500)
		}
		log.Debug("provisioner file staged", "file", filepath.Base(pf))
	}

	// Step 5: Run score-k8s init (fresh workloads only).
	if isFresh {
		log.Info("step 5: running score-k8s init")
		if err := score.RunInit(ctx, workDir); err != nil {
			log.Error("score-k8s init failed", "error", err)
			return nil, oerr.New(oerr.CodeScoreGenerateFailed, "score-k8s init failed", err.Error(), "generate", 500)
		}
		log.Debug("score-k8s init completed")
	}

	// Step 6: Run score-k8s generate.
	log.Info("step 6: running score-k8s generate")
	manifestsFile := filepath.Join(workDir, "manifests.yaml")
	genOutput, genErr := score.RunGenerate(ctx, workDir, manifestsFile)
	if genErr != nil {
		log.Error("score-k8s generate failed", "error", genErr, "output", genOutput)
		meta := &state.DeployMeta{
			LastDeployedAt:   time.Now().UTC(),
			LastDeployStatus: "generate_failed",
			DeployersRun:     "",
		}
		_ = r.Backend.PushMeta(ctx, req.Org, req.Env, req.Workload, meta)
		return nil, oerr.New(oerr.CodeScoreGenerateFailed,
			"score-k8s generate failed",
			fmt.Sprintf("%s\n%s", genErr.Error(), genOutput),
			"generate", 500)
	}
	log.Debug("score-k8s generate completed", "manifests_file", manifestsFile)

	// Step 7: Re-read current state ETag and check for conflict.
	// ETag check is done after generate (which is purely local) to avoid an extra
	// network round-trip before the fast local generate step.
	log.Debug("step 7: checking for concurrent state modification")
	_, currentETag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		log.Error("failed to re-read state for ETag check", "error", err)
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to re-read state for ETag check", err.Error(), "state", 500)
	}
	if !isFresh && currentETag != etag {
		log.Error("state conflict — concurrent deploy modified state", "original_etag", etag, "current_etag", currentETag)
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
	log.Info("step 8: running deployer", r.Deployer)
	deployReq := deployer.DeployRequest{
		Org:           req.Org,
		Env:           req.Env,
		Workload:      req.Workload,
		ManifestsPath: manifestsFile,
	}
	var deployErr error

	if err := r.Deployer.Deploy(ctx, deployReq); err != nil {
		log.Error("deployer failed", "deployer", r.Deployer.Name(), "error", err)
		deployErr = fmt.Errorf("deployer %q failed: %w", r.Deployer.Name(), err)

	}
	log.Info("deployer succeeded", "deployer", r.Deployer.Name())

	deployStatus := "success"
	if deployErr != nil {
		deployStatus = "deploy_failed"
	}

	// Step 9: Push state.yaml with ETag precondition.
	log.Debug("step 9: pushing state.yaml to backend")
	if updatedStateYAML != nil {
		if err := r.Backend.PushState(ctx, req.Org, req.Env, req.Workload, updatedStateYAML, etag); err != nil {
			if _, ok := err.(*state.StateConflictError); ok {
				log.Error("state conflict on push", "error", err)
				return nil, oerr.New(oerr.CodeStateConflict, err.Error(), "", "state", 409)
			}
			log.Error("failed to push state.yaml", "error", err)
			return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push state.yaml", err.Error(), "state", 500)
		}
		log.Debug("state.yaml pushed successfully")
	}

	// Step 10: Push artifacts (last-write-wins).
	log.Debug("step 10: pushing artifacts to backend")
	meta := &state.DeployMeta{
		LastDeployedAt:   time.Now().UTC(),
		LastDeployStatus: deployStatus,
		DeployersRun:     r.Deployer.Name(),
	}
	if err := r.Backend.PushArtifacts(ctx, req.Org, req.Env, req.Workload, req.ScoreYAML, manifestsYAML, meta); err != nil {
		log.Error("failed to push artifacts", "error", err)
		return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push artifacts", err.Error(), "state", 500)
	}

	// Steps 11–12: workDir and provTmp cleanup deferred above; return result.
	log.Info("deploy finished", "org", req.Org, "env", req.Env, "workload", req.Workload,
		"status", deployStatus, "deployer_run", r.Deployer.Name())

	result := &DeployResult{
		Org:          req.Org,
		Env:          req.Env,
		Workload:     req.Workload,
		Status:       deployStatus,
		DeployersRun: r.Deployer.Name(),
		DeployedAt:   meta.LastDeployedAt,
	}
	if deployErr != nil {
		return result, oerr.New(oerr.CodeDeployFailed, deployErr.Error(), "", "deploy", 500)
	}
	return result, nil
}
