package score

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
)

// RunInit runs `score-k8s init` in workDir.
// Only called for fresh workloads (no existing state).
func RunInit(ctx context.Context, workDir string) error {
	log := logger.Get()
	log.Debug("exec: score-k8s init", "work_dir", workDir)
	cmd := exec.CommandContext(ctx, "score-k8s", "init")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("score-k8s init failed: %w\n%s", err, string(out))
	}
	if len(out) > 0 {
		log.Debug("score-k8s init output", "output", string(out))
	}
	return nil
}

// RunGenerate runs `score-k8s generate score.yaml --output <outputFile>` in workDir.
// If imagePath is non-empty it is passed as `--image <imagePath>` to override the container image.
// Returns the combined output alongside any error so callers can surface it.
func RunGenerate(ctx context.Context, workDir, outputFile, imagePath string) (string, error) {
	log := logger.Get()
	log.Debug("exec: score-k8s generate", "work_dir", workDir, "output_file", outputFile)
	args := []string{"generate", "score.yaml", "--output", outputFile}
	if imagePath != "" {
		args = append(args, "--image", imagePath)
		log.Debug("score-k8s generate: overriding image", "image", imagePath)
	}
	cmd := exec.CommandContext(ctx, "score-k8s", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("score-k8s generate exited non-zero: %w", err)
	}
	if len(out) > 0 {
		log.Debug("score-k8s generate output", "output", string(out))
	}
	return "", nil
}
