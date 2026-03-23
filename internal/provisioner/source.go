package provisioner

import "context"

// ProvisionerSource syncs *.provisioners.yaml files into a destination directory.
// The destination is the .score-k8s/ subdirectory of the temp working directory.
type ProvisionerSource interface {
	Sync(ctx context.Context, destDir string) error
}
