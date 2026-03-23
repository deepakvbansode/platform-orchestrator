package deployer

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/deepakvbansode/platform-orchestrator/internal/config"
	"github.com/deepakvbansode/platform-orchestrator/internal/interpolate"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
)

// KubectlDeployer applies manifests using kubectl apply.
type KubectlDeployer struct {
	name string
	cfg  config.KubectlConfig
}

func NewKubectlDeployer(name string, cfg config.KubectlConfig) *KubectlDeployer {
	return &KubectlDeployer{name: name, cfg: cfg}
}

func (d *KubectlDeployer) Name() string { return d.name }

func (d *KubectlDeployer) Deploy(ctx context.Context, req DeployRequest) error {
	log := logger.Get()
	vars := map[string]string{
		"org":      req.Org,
		"env":      req.Env,
		"workload": req.Workload,
	}

	args := []string{"apply", "-f", req.ManifestsPath}

	ns := interpolate.Expand(d.cfg.Namespace, vars)
	if ns != "" {
		args = append(args, "--namespace", ns)
	}
	kctx := interpolate.Expand(d.cfg.Context, vars)
	if kctx != "" {
		args = append(args, "--context", kctx)
	}
	kconfig := interpolate.Expand(d.cfg.Kubeconfig, vars)
	if kconfig != "" {
		args = append(args, "--kubeconfig", kconfig)
	}

	log.Debug("exec: kubectl apply", "namespace", ns, "context", kctx, "manifests", req.ManifestsPath)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, string(out))
	}
	if len(out) > 0 {
		log.Debug("kubectl apply output", "output", string(out))
	}
	return nil
}
