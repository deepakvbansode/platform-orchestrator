package score

import (
	"context"
	"fmt"
	"os/exec"
)

// RunInit runs `score-k8s init` in workDir.
// Only called for fresh workloads (no existing state).
func RunInit(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "score-k8s", "init")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("score-k8s init failed: %w\n%s", err, string(out))
	}
	return nil
}

// RunGenerate runs `score-k8s generate --output <outputFile>` in workDir.
// Returns the combined output alongside any error so callers can surface it.
func RunGenerate(ctx context.Context, workDir, outputFile string) (string, error) {
	cmd := exec.CommandContext(ctx, "score-k8s", "generate", "--output", outputFile)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("score-k8s generate exited non-zero: %w", err)
	}
	return "", nil
}
