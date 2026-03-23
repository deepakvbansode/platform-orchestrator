package cmd

import (
	"context"
	"fmt"

	"github.com/score-spec/score-orchestrator/internal/config"
	"github.com/score-spec/score-orchestrator/internal/deployer"
	"github.com/score-spec/score-orchestrator/internal/pipeline"
	"github.com/score-spec/score-orchestrator/internal/provisioner"
	"github.com/score-spec/score-orchestrator/internal/state"
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

func buildDeployers(cfg *config.Config) ([]deployer.Deployer, error) {
	var deployers []deployer.Deployer
	for _, dc := range cfg.Deployers {
		switch dc.Type {
		case "kubectl":
			deployers = append(deployers, deployer.NewKubectlDeployer(dc.Name, dc.Kubectl))
		case "git":
			deployers = append(deployers, deployer.NewGitDeployer(dc.Name, dc.Git))
		case "webhook":
			deployers = append(deployers, deployer.NewWebhookDeployer(dc.Name, dc.Webhook))
		default:
			return nil, fmt.Errorf("unknown deployer type: %q (must be kubectl, git, or webhook)", dc.Type)
		}
	}
	return deployers, nil
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
		Deployers:   deployers,
	}, nil
}
