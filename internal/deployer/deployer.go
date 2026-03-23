package deployer

import "context"

// DeployRequest carries everything a deployer needs to apply a workload.
type DeployRequest struct {
	Org           string
	Env           string
	Workload      string
	ManifestsPath string // absolute path to manifests.yaml in temp working dir
}

// Deployer applies a generated manifest to a target system.
type Deployer interface {
	Name() string
	Deploy(ctx context.Context, req DeployRequest) error
}
