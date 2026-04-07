package action

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// execInput is the JSON object written to the provisioner's stdin.
// Shape matches the provisioner interface contract in the Platform Action Spec.
type execInput struct {
	UID          string                 `json:"uid"`
	ResourceType string                 `json:"resourceType"`
	Class        string                 `json:"class"`
	ID           string                 `json:"id"`
	Operation    string                 `json:"operation"`
	Params       map[string]interface{} `json:"params"`
	Metadata     execMetadata           `json:"metadata"`
	Context      ActionContext           `json:"context"`
	State        map[string]interface{} `json:"state"`
	Shared       map[string]interface{} `json:"shared"`
}

type execMetadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

// execOutput is the JSON object read from the provisioner's stdout on success.
type execOutput struct {
	Outputs map[string]string      `json:"outputs"`
	State   map[string]interface{} `json:"state"`
	Message string                 `json:"message"`
}

// execError is the JSON object read from stdout on failure.
type execError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// ExecExecutor runs cmd:// provisioners as subprocesses.
type ExecExecutor struct{}

// Execute forks the provisioner binary, writes JSON to its stdin, and reads
// the result from stdout. Returns outputs, a human-readable message, and any
// error (including non-zero exit codes and validation failures).
func (e *ExecExecutor) Execute(ctx context.Context, prov *ActionProvisioner, req ActionRequest) (outputs map[string]string, message string, err error) {
	binaryPath, err := resolveBinaryPath(prov.URIPath())
	if err != nil {
		return nil, "", fmt.Errorf("resolve cmd:// binary: %w", err)
	}

	args := append([]string{}, prov.Args...)
	cmd := exec.CommandContext(ctx, binaryPath, args...)

	input := execInput{
		UID:          req.Spec.Metadata.Name,
		ResourceType: req.Spec.Spec.ResourceType,
		Class:        "",
		ID:           "",
		Operation:    req.Spec.Spec.Operation,
		Params:       req.Spec.Spec.Properties,
		Metadata: execMetadata{
			Name:   req.Spec.Metadata.Name,
			Labels: req.Spec.Metadata.Labels,
		},
		Context: req.Context,
		State:   map[string]interface{}{},
		Shared:  map[string]interface{}{},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, "", fmt.Errorf("marshal provisioner input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Always log stderr regardless of exit code.
	if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
		// stderr is captured and can be appended to the error message.
		_ = stderrStr
	}

	if runErr != nil {
		// Try to decode the structured error from stdout.
		var errOut execError
		if jsonErr := json.Unmarshal(stdout.Bytes(), &errOut); jsonErr == nil && errOut.Error != "" {
			detail := errOut.Error
			if errOut.Code != "" {
				detail = fmt.Sprintf("[%s] %s", errOut.Code, detail)
			}
			if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
				detail += "\n" + stderrStr
			}
			return nil, "", fmt.Errorf("provisioner failed: %s", detail)
		}
		// Fall back to generic error.
		detail := runErr.Error()
		if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
			detail += ": " + stderrStr
		}
		return nil, "", fmt.Errorf("provisioner exited with error: %s", detail)
	}

	var out execOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, "", fmt.Errorf("decode provisioner stdout: %w (raw: %s)", err, stdout.String())
	}
	return out.Outputs, out.Message, nil
}

// resolveBinaryPath resolves a cmd:// URI path to an executable path.
//   - Paths starting with "/" are used as-is (absolute).
//   - Paths starting with "./" or "../" are resolved relative to the working dir.
//   - Everything else is looked up on PATH.
func resolveBinaryPath(uriPath string) (string, error) {
	if filepath.IsAbs(uriPath) {
		return uriPath, nil
	}
	if strings.HasPrefix(uriPath, "./") || strings.HasPrefix(uriPath, "../") {
		abs, err := filepath.Abs(uriPath)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("binary not found at %s: %w", abs, err)
		}
		return abs, nil
	}
	// Look up on PATH.
	resolved, err := exec.LookPath(uriPath)
	if err != nil {
		return "", fmt.Errorf("binary %q not found on PATH: %w", uriPath, err)
	}
	return resolved, nil
}
