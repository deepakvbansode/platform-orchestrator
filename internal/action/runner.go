package action

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/deepakvbansode/platform-orchestrator/internal/deployer"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
	"github.com/deepakvbansode/platform-orchestrator/internal/provisioner"
)

var actionNameRe = regexp.MustCompile(`^[a-z][a-z0-9\-\.]{0,62}$`)

// Runner orchestrates a self-service action from spec to deployment.
type Runner struct {
	// Provisioner syncs provisioner YAML files (reused from the deploy pipeline).
	Provisioner provisioner.ProvisionerSource
	// Deployer applies generated manifests (reused from the deploy pipeline).
	Deployer deployer.Deployer
	// State persists action execution state.
	State ActionStateBackend
	// Namespace is injected as .Namespace into template provisioner contexts.
	Namespace string
}

// Run executes the action pipeline for the given request.
func (r *Runner) Run(ctx context.Context, req ActionRequest) (*ActionResult, error) {
	log := logger.Get()
	name := req.Spec.Metadata.Name
	log.Info("action started", "name", name, "resourceType", req.Spec.Spec.ResourceType, "operation", req.Spec.Spec.Operation)

	// Step 1: Validate the ActionSpec structure.
	log.Debug("step 1: validating action spec")
	if err := validateSpec(req.Spec); err != nil {
		return nil, fmt.Errorf("invalid action spec: %w", err)
	}

	// Step 2: Persist initial "submitted" state.
	initState := &ActionState{
		ActionID:    name,
		Status:      StatusSubmitted,
		RequestedAt: time.Now().UTC(),
	}
	if err := r.State.PutState(ctx, name, initState); err != nil {
		log.Error("failed to persist initial action state", "error", err)
		// Non-fatal: continue execution.
	}

	// Step 3: Sync provisioners into a temp dir.
	log.Debug("step 3: syncing provisioners")
	provTmp, err := os.MkdirTemp("", "action-provisioners-*")
	if err != nil {
		return r.failWith(ctx, name, fmt.Errorf("create provisioner temp dir: %w", err))
	}
	defer os.RemoveAll(provTmp)

	if err := r.Provisioner.Sync(ctx, provTmp); err != nil {
		return r.failWith(ctx, name, fmt.Errorf("sync provisioners: %w", err))
	}
	log.Debug("provisioners synced", "tmp_dir", provTmp)

	// Step 4: Load action provisioners and find a match.
	log.Debug("step 4: matching provisioner")
	provisioners, err := LoadActionProvisioners(provTmp)
	if err != nil {
		return r.failWith(ctx, name, fmt.Errorf("load action provisioners: %w", err))
	}
	prov, err := Match(provisioners, req.Spec.Spec.ResourceType, "", "")
	if err != nil {
		return r.failWith(ctx, name, err)
	}
	log.Info("provisioner matched", "uri", prov.URI, "resourceType", prov.ResourceType)

	// Build a stable GUID for this action execution.
	guid := generateGUID()

	result, execErr := r.execute(ctx, prov, req, guid)
	if execErr != nil {
		return r.failWith(ctx, name, execErr)
	}

	// Step 7: Persist final state.
	finalState := &ActionState{
		ActionID:    name,
		Status:      result.Status,
		Outputs:     result.Outputs,
		Message:     result.Message,
		RequestedAt: initState.RequestedAt,
	}
	if err := r.State.PutState(ctx, name, finalState); err != nil {
		log.Error("failed to persist final action state", "error", err)
		// Non-fatal: return result anyway.
	}

	log.Info("action completed", "name", name, "status", result.Status)
	return result, nil
}

// execute dispatches to the appropriate executor based on URI scheme.
func (r *Runner) execute(ctx context.Context, prov *ActionProvisioner, req ActionRequest, guid string) (*ActionResult, error) {
	log := logger.Get()
	name := req.Spec.Metadata.Name

	tctx := TemplateContext{
		Params:         req.Spec.Spec.Properties,
		Operation:      defaultOperation(req.Spec.Spec.Operation),
		Guid:           guid,
		Uid:            name,
		SourceWorkload: name,
		Namespace:      r.Namespace,
		Context:        req.Context,
		State:          make(map[string]string),
	}

	_ = log
	switch prov.URIScheme() {
	case "template":
		return r.executeTemplate(ctx, prov, req, tctx)
	case "cmd":
		return r.executeCmd(ctx, prov, req)
	default:
		return nil, fmt.Errorf("unsupported provisioner URI scheme %q in %q", prov.URIScheme(), prov.URI)
	}
}

func (r *Runner) executeTemplate(ctx context.Context, prov *ActionProvisioner, req ActionRequest, tctx TemplateContext) (*ActionResult, error) {
	log := logger.Get()
	name := req.Spec.Metadata.Name

	// Create a temp working directory for manifests.
	workDir, err := os.MkdirTemp("", "action-workdir-*")
	if err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Step 5a: Evaluate Go templates → manifests.yaml.
	log.Debug("step 5a: evaluating template provisioner")
	executor := &TemplateExecutor{}
	manifestsPath, err := executor.Execute(prov, tctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("template provisioner failed: %w", err)
	}
	log.Debug("manifests generated", "path", manifestsPath)

	// Step 6: Deploy the generated manifests.
	log.Info("step 6: deploying manifests", "deployer", r.Deployer.Name())
	deployReq := deployer.DeployRequest{
		Org:           "actions",
		Env:           req.Spec.Spec.ResourceType,
		Workload:      name,
		ManifestsPath: manifestsPath,
	}
	if err := r.Deployer.Deploy(ctx, deployReq); err != nil {
		return nil, fmt.Errorf("deploy failed: %w", err)
	}
	log.Info("manifests deployed successfully")

	return &ActionResult{
		ActionID: name,
		Status:   StatusDeployed,
		Message:  fmt.Sprintf("manifests deployed via %s", r.Deployer.Name()),
	}, nil
}

func (r *Runner) executeCmd(ctx context.Context, prov *ActionProvisioner, req ActionRequest) (*ActionResult, error) {
	log := logger.Get()
	log.Debug("step 5b: executing cmd:// provisioner", "uri", prov.URI)

	executor := &ExecExecutor{}
	outputs, message, err := executor.Execute(ctx, prov, req)
	if err != nil {
		return nil, fmt.Errorf("cmd provisioner failed: %w", err)
	}
	log.Info("cmd provisioner succeeded", "message", message)

	return &ActionResult{
		ActionID: req.Spec.Metadata.Name,
		Status:   StatusCompleted,
		Outputs:  outputs,
		Message:  message,
	}, nil
}

// failWith persists a "failed" state and returns the error.
func (r *Runner) failWith(ctx context.Context, name string, err error) (*ActionResult, error) {
	log := logger.Get()
	log.Error("action failed", "name", name, "error", err)

	failedState := &ActionState{
		ActionID: name,
		Status:   StatusFailed,
		Message:  err.Error(),
	}
	if putErr := r.State.PutState(ctx, name, failedState); putErr != nil {
		log.Error("failed to persist error state", "error", putErr)
	}
	return nil, err
}

// validateSpec checks required fields and name format.
func validateSpec(spec ActionSpec) error {
	if spec.APIVersion != "platform.company.io/v1" {
		return fmt.Errorf("unsupported apiVersion %q (expected platform.company.io/v1)", spec.APIVersion)
	}
	if spec.Kind != "Action" {
		return fmt.Errorf("unsupported kind %q (expected Action)", spec.Kind)
	}
	if spec.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if !actionNameRe.MatchString(spec.Metadata.Name) {
		return fmt.Errorf("metadata.name %q must match ^[a-z][a-z0-9\\-\\.]{0,62}$", spec.Metadata.Name)
	}
	if spec.Spec.ResourceType == "" {
		return fmt.Errorf("spec.resourceType is required")
	}
	if spec.Spec.Properties == nil {
		return fmt.Errorf("spec.properties is required")
	}
	return nil
}

// defaultOperation returns "create" when operation is empty (per spec default).
func defaultOperation(op string) string {
	if op == "" {
		return "create"
	}
	return op
}

// generateGUID returns a 32-character random hex string used as a stable GUID
// for a single action execution.
func generateGUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based string.
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(b)
}
