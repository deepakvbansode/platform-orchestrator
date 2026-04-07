package cmd

import (
	"context"
	"fmt"

	"github.com/deepakvbansode/platform-orchestrator/internal/action"
	"github.com/deepakvbansode/platform-orchestrator/internal/config"
	"github.com/deepakvbansode/platform-orchestrator/internal/deployer"
	"github.com/deepakvbansode/platform-orchestrator/internal/pipeline"
	"github.com/deepakvbansode/platform-orchestrator/internal/provisioner"
	"github.com/deepakvbansode/platform-orchestrator/internal/state"
)

func buildBackend(ctx context.Context, cfg *config.Config) (state.StateBackend, error) {
	switch cfg.State.Backend {
	case "file":
		return state.NewFileBackend(cfg.State.File.BasePath), nil
	case "s3":
		return state.NewS3Backend(ctx, cfg.State.S3.Bucket, cfg.State.S3.Prefix, cfg.State.S3.Region)
	default:
		return nil, fmt.Errorf("unknown state backend: %q (must be file or s3)", cfg.State.Backend)
	}
}

func buildProvisionerSource(cfg *config.Config) (provisioner.ProvisionerSource, error) {
	switch cfg.Provisioners.Source {
	case "local":
		return provisioner.NewLocalSource(cfg.Provisioners.Local.Path), nil
	case "git":
		return provisioner.NewGitSource(cfg.Provisioners.Git), nil
	default:
		return nil, fmt.Errorf("unknown provisioner source: %q (must be local or git)", cfg.Provisioners.Source)
	}
}

func buildDeployers(cfg *config.Config) (deployer.Deployer, error) {

	switch cfg.Deployer.Source {
	case "kubectl":
		return deployer.NewKubectlDeployer("kubectl", cfg.Deployer.Kubectl), nil
	case "git":
		return deployer.NewGitDeployer("git", cfg.Deployer.Git), nil
	case "webhook":
		return deployer.NewWebhookDeployer("webhook", cfg.Deployer.Webhook), nil
	default:
		return nil, fmt.Errorf("unknown deployer type: %q (must be kubectl, git, or webhook)", cfg.Deployer.Source)
	}

}

func buildRunner(cfg *config.Config) (*pipeline.Runner, error) {
	backend, err := buildBackend(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	provSource, err := buildProvisionerSource(cfg)
	if err != nil {
		return nil, err
	}
	deployers, err := buildDeployers(cfg)
	if err != nil {
		return nil, err
	}
	return &pipeline.Runner{
		Backend:     backend,
		Provisioner: provSource,
		Deployer:    deployers,
	}, nil
}

func buildActionStateBackend(cfg *config.Config) action.ActionStateBackend {
	// Reuse the file state base path so action state lives alongside workload state.
	basePath := cfg.State.File.BasePath
	if basePath == "" {
		basePath = ".state"
	}
	return action.NewFileActionStateBackend(basePath)
}

func buildActionRunner(cfg *config.Config, provSource provisioner.ProvisionerSource, d deployer.Deployer, stateBackend action.ActionStateBackend) *action.Runner {
	return &action.Runner{
		Provisioner: provSource,
		Deployer:    d,
		State:       stateBackend,
		Namespace:   cfg.Action.Namespace,
	}
}
