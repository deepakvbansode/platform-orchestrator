# Score Orchestrator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a thin Go orchestrator that wraps `score-k8s` to add manifest deployment, scoped state management, and provisioner syncing.

**Architecture:** Pipeline-per-command approach — every deploy request runs a strict 12-step pipeline (validate → pull state → sync provisioners → generate → deploy → push state). State is namespaced by `org/env/workload` in a pluggable backend (file or S3). A single binary exposes both a CLI (`score-orchestrator deploy`) and an HTTP REST server (`score-orchestrator server`).

**Tech Stack:** Go, cobra (CLI), chi (HTTP), viper (config), aws-sdk-go-v2 (S3), shell out to `git`, `kubectl`, `score-k8s`

**Note:** No unit test files are written anywhere in this project.

---

## File Map

```
score-orchestrator/
├── main.go                               # entry point — calls cmd.Execute()
├── go.mod
├── orchestrator.yaml                     # example platform config
├── cmd/
│   ├── root.go                           # cobra root, config flag, loads config
│   ├── deploy.go                         # `deploy` subcommand → calls pipeline.Run()
│   ├── server.go                         # `server` subcommand → starts api.Server
│   └── wire.go                           # shared wiring: buildBackend, buildProvisionerSource, buildDeployers, buildRunner
├── internal/
│   ├── config/
│   │   └── config.go                     # Config struct + Load() via viper
│   ├── interpolate/
│   │   └── interpolate.go                # Expand(): ${org/env/workload} and $env{VAR}
│   ├── errors/
│   │   └── errors.go                     # OrchestratorError{Code,Message,Detail,Stage,HTTPStatus}
│   ├── state/
│   │   ├── backend.go                    # StateBackend interface, DeployMeta, WorkloadFiles, StateConflictError
│   │   ├── file.go                       # FileBackend — reads/writes files under base_path/org/env/workload/
│   │   └── s3.go                         # S3Backend — same layout in S3; ETag via S3 object ETag
│   ├── provisioner/
│   │   ├── source.go                     # ProvisionerSource interface
│   │   ├── local.go                      # LocalSource — copies *.provisioners.yaml from local path
│   │   └── git.go                        # GitSource — shallow clone then copy *.provisioners.yaml
│   ├── score/
│   │   └── runner.go                     # RunInit(workDir), RunGenerate(workDir, output) — shell out to score-k8s
│   ├── deployer/
│   │   ├── deployer.go                   # Deployer interface + DeployRequest type
│   │   ├── kubectl.go                    # KubectlDeployer — kubectl apply -f manifests.yaml
│   │   ├── git.go                        # GitDeployer — clone, copy manifest, commit, push
│   │   └── webhook.go                    # WebhookDeployer — HTTP POST with timeout + headers
│   └── pipeline/
│       ├── request.go                    # DeployRequest, DeployResult types
│       ├── validate.go                   # ValidateInput() — identity regex + YAML parse check
│       └── pipeline.go                   # Run() — all 12 pipeline steps
└── api/
    ├── errors.go                         # writeError() helper, error JSON shape
    ├── handlers.go                       # handleDeploy, handleStatus, handleManifest, handleHealthz
    └── server.go                         # chi router wiring + http.Server start
```

---

## Task 1: Go module init and project skeleton

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: all directories listed in the file map (empty, to establish structure)

- [ ] **Step 1: Initialize the Go module**

```bash
cd /Users/deepak.bansodegruve.ai/Documents/projects/platform-score-orchestrator
go mod init github.com/score-spec/score-orchestrator
```

- [ ] **Step 2: Write `main.go`**

```go
package main

import "github.com/score-spec/score-orchestrator/cmd"

func main() {
	cmd.Execute()
}
```

- [ ] **Step 3: Create all package directories**

```bash
mkdir -p cmd internal/config internal/interpolate internal/errors \
  internal/state internal/provisioner internal/score \
  internal/deployer internal/pipeline api
```

- [ ] **Step 4: Add all dependencies to go.mod**

```bash
go get github.com/spf13/cobra@latest
go get github.com/spf13/viper@latest
go get github.com/mitchellh/mapstructure@latest
go get github.com/go-chi/chi/v5@latest
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go get gopkg.in/yaml.v3@latest
```

- [ ] **Step 5: Commit**

```bash
git init
git add go.mod go.sum main.go
git commit -m "chore: initialize go module and project skeleton"
```

---

## Task 2: Config package

**Files:**
- Create: `internal/config/config.go`

The config package owns the `orchestrator.yaml` schema as Go structs and a `Load(path string)` function using viper.

- [ ] **Step 1: Write `internal/config/config.go`**

```go
package config

import (
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

type Config struct {
	State        StateConfig      `mapstructure:"state"`
	Provisioners ProvisionersConfig `mapstructure:"provisioners"`
	Deployers    []DeployerConfig `mapstructure:"deployers"`
}

type StateConfig struct {
	Backend string          `mapstructure:"backend"` // "file" | "s3"
	File    FileStateConfig `mapstructure:"file"`
	S3      S3StateConfig   `mapstructure:"s3"`
}

type FileStateConfig struct {
	BasePath string `mapstructure:"base_path"`
}

type S3StateConfig struct {
	Bucket string `mapstructure:"bucket"`
	Prefix string `mapstructure:"prefix"`
	Region string `mapstructure:"region"`
}

type ProvisionersConfig struct {
	Source string          `mapstructure:"source"` // "git" | "local"
	Git    GitProvConfig   `mapstructure:"git"`
	Local  LocalProvConfig `mapstructure:"local"`
}

type GitProvConfig struct {
	URL  string     `mapstructure:"url"`
	Ref  string     `mapstructure:"ref"`
	Path string     `mapstructure:"path"`
	Auth AuthConfig `mapstructure:"auth"`
}

type LocalProvConfig struct {
	Path string `mapstructure:"path"`
}

type AuthConfig struct {
	Type       string `mapstructure:"type"`         // "ssh" | "https"
	SSHKeyFile string `mapstructure:"ssh_key_file"`
	TokenEnv   string `mapstructure:"token_env"`
}

type DeployerConfig struct {
	Name    string            `mapstructure:"name"`
	Type    string            `mapstructure:"type"` // "kubectl" | "git" | "webhook"
	Kubectl KubectlConfig     `mapstructure:"kubectl"`
	Git     GitDeployerConfig `mapstructure:"git"`
	Webhook WebhookConfig     `mapstructure:"webhook"`
}

type KubectlConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
	Context    string `mapstructure:"context"`
}

type GitDeployerConfig struct {
	URL  string     `mapstructure:"url"`
	Ref  string     `mapstructure:"ref"`
	Path string     `mapstructure:"path"`
	Auth AuthConfig `mapstructure:"auth"`
}

type WebhookConfig struct {
	URL            string            `mapstructure:"url"`
	Method         string            `mapstructure:"method"`
	TimeoutSeconds int               `mapstructure:"timeout_seconds"`
	Headers        map[string]string `mapstructure:"headers"`
	Body           string            `mapstructure:"body"`
}

// Load reads the config file at path and returns a populated Config.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "mapstructure"
	}); err != nil {
		return nil, err
	}
	return &cfg, nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/config/...
```

Expected: exits 0, no output.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add config package with orchestrator.yaml schema"
```

---

## Task 3: Interpolation package

**Files:**
- Create: `internal/interpolate/interpolate.go`

Handles two interpolation syntaxes:
- `${key}` — request-context variables (org, env, workload); keys are lowercase
- `$env{VAR}` — OS environment variables

- [ ] **Step 1: Write `internal/interpolate/interpolate.go`**

```go
package interpolate

import (
	"os"
	"regexp"
)

var (
	envPattern     = regexp.MustCompile(`\$env\{([^}]+)\}`)
	contextPattern = regexp.MustCompile(`\$\{([^}]+)\}`)
)

// Expand replaces $env{VAR} with os.Getenv(VAR) and ${key} with ctx[key].
// Unknown context keys are left as-is. Unknown env vars resolve to "".
func Expand(s string, ctx map[string]string) string {
	// Replace $env{VAR} first to avoid collision with ${...} pattern.
	s = envPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := envPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return os.Getenv(sub[1])
	})
	// Replace ${key} from context map.
	s = contextPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := contextPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		if val, ok := ctx[sub[1]]; ok {
			return val
		}
		return match
	})
	return s
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/interpolate/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/interpolate/interpolate.go
git commit -m "feat: add interpolation for request-context and env vars"
```

---

## Task 4: Error types

**Files:**
- Create: `internal/errors/errors.go`

- [ ] **Step 1: Write `internal/errors/errors.go`**

```go
package errors

import "fmt"

// Error codes as defined in the spec.
const (
	CodeInvalidInput          = "INVALID_INPUT"
	CodeInvalidScore          = "INVALID_SCORE"
	CodeStatePullFailed       = "STATE_PULL_FAILED"
	CodeProvisionerSyncFailed = "PROVISIONER_SYNC_FAILED"
	CodeScoreGenerateFailed   = "SCORE_GENERATE_FAILED"
	CodeStateConflict         = "STATE_CONFLICT"
	CodeDeployFailed          = "DEPLOY_FAILED"
	CodeStatePushFailed       = "STATE_PUSH_FAILED"
	CodeWorkloadNotFound      = "WORKLOAD_NOT_FOUND"
)

// OrchestratorError is the canonical error type for all pipeline failures.
type OrchestratorError struct {
	Code       string
	Message    string
	Detail     string
	Stage      string
	HTTPStatus int
}

func (e *OrchestratorError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func New(code, message, detail, stage string, httpStatus int) *OrchestratorError {
	return &OrchestratorError{
		Code:       code,
		Message:    message,
		Detail:     detail,
		Stage:      stage,
		HTTPStatus: httpStatus,
	}
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/errors/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/errors/errors.go
git commit -m "feat: add orchestrator error types and codes"
```

---

## Task 5: State backend — interface and shared types

**Files:**
- Create: `internal/state/backend.go`

- [ ] **Step 1: Write `internal/state/backend.go`**

```go
package state

import (
	"context"
	"time"
)

// DeployMeta is stored in deploy_meta.json alongside state.yaml.
type DeployMeta struct {
	LastDeployedAt   time.Time `json:"last_deployed_at"`
	LastDeployStatus string    `json:"last_deploy_status"` // "success" | "deploy_failed" | "generate_failed"
	DeployersRun     []string  `json:"deployers_run"`
}

// WorkloadFiles holds the files needed by the status endpoint.
type WorkloadFiles struct {
	ScoreYAML  []byte      // content of score.yaml; nil if not found
	StateYAML  []byte      // content of state.yaml; nil if not found
	DeployMeta *DeployMeta // nil if deploy_meta.json not found
}

// StateConflictError is returned by PushState when the ETag has changed since pull.
type StateConflictError struct{}

func (e *StateConflictError) Error() string {
	return "state was modified by a concurrent deployment, retry"
}

// StateBackend abstracts storage of per-workload state files.
// State is namespaced as {org}/{env}/{workload}/.
type StateBackend interface {
	// PullState reads state.yaml and returns its content + ETag.
	// Returns nil stateYAML and empty etag when the workload has no stored state.
	PullState(ctx context.Context, org, env, workload string) (stateYAML []byte, etag string, err error)

	// PushState writes state.yaml with optimistic concurrency: if the stored ETag
	// no longer matches etag, returns *StateConflictError.
	// Pass etag="" to skip the check (fresh workload).
	PushState(ctx context.Context, org, env, workload string, stateYAML []byte, etag string) error

	// PushMeta writes deploy_meta.json only (last-write-wins). Used on generate failure.
	PushMeta(ctx context.Context, org, env, workload string, meta *DeployMeta) error

	// PushArtifacts writes score.yaml, manifests.yaml, and deploy_meta.json (last-write-wins).
	PushArtifacts(ctx context.Context, org, env, workload string, scoreYAML, manifestsYAML []byte, meta *DeployMeta) error

	// GetStatus reads score.yaml, state.yaml, and deploy_meta.json for the status endpoint.
	// Returns a WorkloadFiles where nil fields mean the file was not found.
	GetStatus(ctx context.Context, org, env, workload string) (*WorkloadFiles, error)

	// GetManifest reads manifests.yaml. Returns nil, nil when not found.
	GetManifest(ctx context.Context, org, env, workload string) ([]byte, error)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/state/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/state/backend.go
git commit -m "feat: add state backend interface and shared types"
```

---

## Task 6: State backend — file implementation

**Files:**
- Create: `internal/state/file.go`

ETag for the file backend is the hex-encoded SHA-256 of the file contents at pull time. On `PushState`, the file is re-read, its SHA-256 is recomputed, and compared to the provided ETag.

- [ ] **Step 1: Write `internal/state/file.go`**

```go
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileBackend stores workload state as files under BasePath/org/env/workload/.
type FileBackend struct {
	BasePath string
}

func NewFileBackend(basePath string) *FileBackend {
	return &FileBackend{BasePath: basePath}
}

func (b *FileBackend) dir(org, env, workload string) string {
	return filepath.Join(b.BasePath, org, env, workload)
}

func (b *FileBackend) path(org, env, workload, filename string) string {
	return filepath.Join(b.dir(org, env, workload), filename)
}

func checksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (b *FileBackend) PullState(ctx context.Context, org, env, workload string) ([]byte, string, error) {
	p := b.path(org, env, workload, "state.yaml")
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read state.yaml: %w", err)
	}
	return data, checksum(data), nil
}

func (b *FileBackend) PushState(ctx context.Context, org, env, workload string, stateYAML []byte, etag string) error {
	p := b.path(org, env, workload, "state.yaml")
	// ETag conflict check: re-read current content and compare.
	if etag != "" {
		current, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read state.yaml for ETag check: %w", err)
		}
		if err == nil && checksum(current) != etag {
			return &StateConflictError{}
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(p, stateYAML, 0644)
}

func (b *FileBackend) PushMeta(ctx context.Context, org, env, workload string, meta *DeployMeta) error {
	p := b.path(org, env, workload, "deploy_meta.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

func (b *FileBackend) PushArtifacts(ctx context.Context, org, env, workload string, scoreYAML, manifestsYAML []byte, meta *DeployMeta) error {
	dir := b.dir(org, env, workload)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "score.yaml"), scoreYAML, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifests.yaml"), manifestsYAML, 0644); err != nil {
		return err
	}
	return b.PushMeta(ctx, org, env, workload, meta)
}

func (b *FileBackend) GetStatus(ctx context.Context, org, env, workload string) (*WorkloadFiles, error) {
	wf := &WorkloadFiles{}
	var err error

	scoreData, err := os.ReadFile(b.path(org, env, workload, "score.yaml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read score.yaml: %w", err)
	}
	wf.ScoreYAML = scoreData

	stateData, err := os.ReadFile(b.path(org, env, workload, "state.yaml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read state.yaml: %w", err)
	}
	wf.StateYAML = stateData

	metaData, err := os.ReadFile(b.path(org, env, workload, "deploy_meta.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read deploy_meta.json: %w", err)
	}
	if metaData != nil {
		var meta DeployMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			return nil, fmt.Errorf("parse deploy_meta.json: %w", err)
		}
		wf.DeployMeta = &meta
	}
	return wf, nil
}

func (b *FileBackend) GetManifest(ctx context.Context, org, env, workload string) ([]byte, error) {
	data, err := os.ReadFile(b.path(org, env, workload, "manifests.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/state/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/state/file.go
git commit -m "feat: add file state backend with SHA-256 ETag conflict detection"
```

---

## Task 7: State backend — S3 implementation

**Files:**
- Create: `internal/state/s3.go`

The S3 backend uses the native S3 ETag (MD5 of the object) for conflict detection. `PushState` uses the `If-Match` conditional header — S3 returns `412 Precondition Failed` if the ETag doesn't match, which is mapped to `StateConflictError`.

- [ ] **Step 1: Write `internal/state/s3.go`**

```go
package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Backend stores workload state in S3 under {Prefix}/{org}/{env}/{workload}/.
type S3Backend struct {
	client *s3.Client
	Bucket string
	Prefix string // optional key prefix, no trailing slash
}

func NewS3Backend(ctx context.Context, bucket, prefix, region string) (*S3Backend, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &S3Backend{
		client: s3.NewFromConfig(cfg),
		Bucket: bucket,
		Prefix: prefix,
	}, nil
}

func (b *S3Backend) key(org, env, workload, filename string) string {
	parts := []string{org, env, workload, filename}
	if b.Prefix != "" {
		parts = append([]string{b.Prefix}, parts...)
	}
	return strings.Join(parts, "/")
}

func (b *S3Backend) getObject(ctx context.Context, key string) ([]byte, string, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", nil
		}
		return nil, "", err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}
	return data, etag, nil
}

func (b *S3Backend) putObject(ctx context.Context, key string, data []byte, ifMatch string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}
	if ifMatch != "" {
		input.IfMatch = aws.String(ifMatch)
	}
	_, err := b.client.PutObject(ctx, input)
	return err
}

func (b *S3Backend) PullState(ctx context.Context, org, env, workload string) ([]byte, string, error) {
	data, etag, err := b.getObject(ctx, b.key(org, env, workload, "state.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("S3 get state.yaml: %w", err)
	}
	return data, etag, nil
}

func (b *S3Backend) PushState(ctx context.Context, org, env, workload string, stateYAML []byte, etag string) error {
	err := b.putObject(ctx, b.key(org, env, workload, "state.yaml"), stateYAML, etag)
	if err != nil {
		// S3 returns a PreconditionFailed error when If-Match doesn't hold.
		var precondFailed *types.PreconditionFailed
		if errors.As(err, &precondFailed) {
			return &StateConflictError{}
		}
		return fmt.Errorf("S3 put state.yaml: %w", err)
	}
	return nil
}

func (b *S3Backend) PushMeta(ctx context.Context, org, env, workload string, meta *DeployMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return b.putObject(ctx, b.key(org, env, workload, "deploy_meta.json"), data, "")
}

func (b *S3Backend) PushArtifacts(ctx context.Context, org, env, workload string, scoreYAML, manifestsYAML []byte, meta *DeployMeta) error {
	if err := b.putObject(ctx, b.key(org, env, workload, "score.yaml"), scoreYAML, ""); err != nil {
		return fmt.Errorf("S3 put score.yaml: %w", err)
	}
	if err := b.putObject(ctx, b.key(org, env, workload, "manifests.yaml"), manifestsYAML, ""); err != nil {
		return fmt.Errorf("S3 put manifests.yaml: %w", err)
	}
	return b.PushMeta(ctx, org, env, workload, meta)
}

func (b *S3Backend) GetStatus(ctx context.Context, org, env, workload string) (*WorkloadFiles, error) {
	wf := &WorkloadFiles{}

	scoreData, _, err := b.getObject(ctx, b.key(org, env, workload, "score.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get score.yaml: %w", err)
	}
	wf.ScoreYAML = scoreData

	stateData, _, err := b.getObject(ctx, b.key(org, env, workload, "state.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get state.yaml: %w", err)
	}
	wf.StateYAML = stateData

	metaData, _, err := b.getObject(ctx, b.key(org, env, workload, "deploy_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("S3 get deploy_meta.json: %w", err)
	}
	if metaData != nil {
		var meta DeployMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			return nil, fmt.Errorf("parse deploy_meta.json: %w", err)
		}
		wf.DeployMeta = &meta
	}
	return wf, nil
}

func (b *S3Backend) GetManifest(ctx context.Context, org, env, workload string) ([]byte, error) {
	data, _, err := b.getObject(ctx, b.key(org, env, workload, "manifests.yaml"))
	if err != nil {
		return nil, fmt.Errorf("S3 get manifests.yaml: %w", err)
	}
	return data, nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/state/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/state/s3.go
git commit -m "feat: add S3 state backend with conditional Put ETag conflict detection"
```

---

## Task 8: Provisioner source — interface and local implementation

**Files:**
- Create: `internal/provisioner/source.go`
- Create: `internal/provisioner/local.go`

- [ ] **Step 1: Write `internal/provisioner/source.go`**

```go
package provisioner

import "context"

// ProvisionerSource syncs *.provisioners.yaml files into a destination directory.
// The destination is the .score-k8s/ subdirectory of the temp working directory.
type ProvisionerSource interface {
	Sync(ctx context.Context, destDir string) error
}
```

- [ ] **Step 2: Write `internal/provisioner/local.go`**

```go
package provisioner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalSource copies *.provisioners.yaml files from a local directory.
type LocalSource struct {
	Path string
}

func NewLocalSource(path string) *LocalSource {
	return &LocalSource{Path: path}
}

func (s *LocalSource) Sync(ctx context.Context, destDir string) error {
	matches, err := filepath.Glob(filepath.Join(s.Path, "*.provisioners.yaml"))
	if err != nil {
		return fmt.Errorf("glob provisioners: %w", err)
	}
	for _, src := range matches {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/provisioner/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/provisioner/source.go internal/provisioner/local.go
git commit -m "feat: add provisioner source interface and local implementation"
```

---

## Task 9: Provisioner source — git implementation

**Files:**
- Create: `internal/provisioner/git.go`

Shallow-clones the repo using `git clone --depth 1 --branch <ref> <url> <tmpDir>`. For SSH auth, sets `GIT_SSH_COMMAND` to use the specified key. For HTTPS, embeds the token in the URL.

- [ ] **Step 1: Write `internal/provisioner/git.go`**

```go
package provisioner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/score-spec/score-orchestrator/internal/config"
)

// GitSource shallow-clones a git repo and copies *.provisioners.yaml from a subpath.
type GitSource struct {
	cfg config.GitProvConfig
}

func NewGitSource(cfg config.GitProvConfig) *GitSource {
	return &GitSource{cfg: cfg}
}

func (s *GitSource) Sync(ctx context.Context, destDir string) error {
	tmpDir, err := os.MkdirTemp("", "score-provisioners-*")
	if err != nil {
		return fmt.Errorf("create temp dir for git clone: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := s.cfg.URL
	env := os.Environ()

	switch s.cfg.Auth.Type {
	case "https":
		token := os.Getenv(s.cfg.Auth.TokenEnv)
		// Embed token in URL: https://token@host/path
		if token != "" {
			// Insert token before the host portion.
			url = embedHTTPSToken(url, token)
		}
	case "ssh":
		if s.cfg.Auth.SSHKeyFile != "" {
			env = append(env, fmt.Sprintf(
				"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no",
				s.cfg.Auth.SSHKeyFile,
			))
		}
	}

	ref := s.cfg.Ref
	if ref == "" {
		ref = "main"
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", ref, url, tmpDir)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}

	srcDir := tmpDir
	if s.cfg.Path != "" {
		srcDir = filepath.Join(tmpDir, s.cfg.Path)
	}

	matches, err := filepath.Glob(filepath.Join(srcDir, "*.provisioners.yaml"))
	if err != nil {
		return fmt.Errorf("glob provisioners: %w", err)
	}
	for _, src := range matches {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

// embedHTTPSToken inserts a token into an HTTPS git URL.
// "https://github.com/org/repo.git" → "https://token@github.com/org/repo.git"
func embedHTTPSToken(rawURL, token string) string {
	const httpsPrefix = "https://"
	if len(rawURL) > len(httpsPrefix) && rawURL[:len(httpsPrefix)] == httpsPrefix {
		return httpsPrefix + token + "@" + rawURL[len(httpsPrefix):]
	}
	return rawURL
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/provisioner/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/provisioner/git.go
git commit -m "feat: add git provisioner source with SSH and HTTPS auth"
```

---

## Task 10: Score runner

**Files:**
- Create: `internal/score/runner.go`

Thin wrappers around `score-k8s init` and `score-k8s generate`. Both run in the temp working directory and capture stderr for error messages.

- [ ] **Step 1: Write `internal/score/runner.go`**

```go
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
// Returns the stderr output alongside the error so callers can surface it.
func RunGenerate(ctx context.Context, workDir, outputFile string) (string, error) {
	cmd := exec.CommandContext(ctx, "score-k8s", "generate", "--output", outputFile)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("score-k8s generate exited non-zero: %w", err)
	}
	return "", nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/score/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/score/runner.go
git commit -m "feat: add score-k8s runner wrapping init and generate"
```

---

## Task 11: Deployer — interface and request types

**Files:**
- Create: `internal/deployer/deployer.go`

- [ ] **Step 1: Write `internal/deployer/deployer.go`**

```go
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
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/deployer/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/deployer/deployer.go
git commit -m "feat: add deployer interface and DeployRequest type"
```

---

## Task 12: Deployer — kubectl

**Files:**
- Create: `internal/deployer/kubectl.go`

Runs `kubectl apply -f <manifests>` with optional `--namespace`, `--context`, `--kubeconfig` flags. Namespace and context values are interpolated from request context before the command runs.

- [ ] **Step 1: Write `internal/deployer/kubectl.go`**

```go
package deployer

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/score-spec/score-orchestrator/internal/config"
	"github.com/score-spec/score-orchestrator/internal/interpolate"
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

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, string(out))
	}
	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/deployer/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/deployer/kubectl.go
git commit -m "feat: add kubectl deployer"
```

---

## Task 13: Deployer — git push

**Files:**
- Create: `internal/deployer/git.go`

Clones the target repo, copies `manifests.yaml` to the configured path, commits, and pushes. Uses the same auth helpers (SSH env var / HTTPS token embedding) as the git provisioner source.

- [ ] **Step 1: Write `internal/deployer/git.go`**

```go
package deployer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/score-spec/score-orchestrator/internal/config"
	"github.com/score-spec/score-orchestrator/internal/interpolate"
)

// GitDeployer clones a git repo, copies the manifest, commits, and pushes.
type GitDeployer struct {
	name string
	cfg  config.GitDeployerConfig
}

func NewGitDeployer(name string, cfg config.GitDeployerConfig) *GitDeployer {
	return &GitDeployer{name: name, cfg: cfg}
}

func (d *GitDeployer) Name() string { return d.name }

func (d *GitDeployer) Deploy(ctx context.Context, req DeployRequest) error {
	vars := map[string]string{"org": req.Org, "env": req.Env, "workload": req.Workload}

	tmpDir, err := os.MkdirTemp("", "score-gitdeploy-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := interpolate.Expand(d.cfg.URL, vars)
	env := os.Environ()

	ref := d.cfg.Ref
	if ref == "" {
		ref = "main"
	}

	switch d.cfg.Auth.Type {
	case "https":
		token := os.Getenv(d.cfg.Auth.TokenEnv)
		if token != "" {
			url = embedHTTPSToken(url, token)
		}
	case "ssh":
		if d.cfg.Auth.SSHKeyFile != "" {
			env = append(env, fmt.Sprintf(
				"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no",
				d.cfg.Auth.SSHKeyFile,
			))
		}
	}

	run := func(dir string, args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w\n%s", args, err, string(out))
		}
		return nil
	}

	if err := run("", "clone", "--depth", "1", "--branch", ref, url, tmpDir); err != nil {
		return err
	}

	destPath := filepath.Join(tmpDir, interpolate.Expand(d.cfg.Path, vars))
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("mkdir dest path: %w", err)
	}

	dst := filepath.Join(destPath, fmt.Sprintf("%s-%s-%s-manifests.yaml", req.Org, req.Env, req.Workload))
	if err := copyFile(req.ManifestsPath, dst); err != nil {
		return fmt.Errorf("copy manifests: %w", err)
	}

	if err := run(tmpDir, "config", "user.email", "score-orchestrator@platform"); err != nil {
		return err
	}
	if err := run(tmpDir, "config", "user.name", "score-orchestrator"); err != nil {
		return err
	}
	if err := run(tmpDir, "add", dst); err != nil {
		return err
	}
	if err := run(tmpDir, "commit", "-m", fmt.Sprintf("deploy: %s/%s/%s", req.Org, req.Env, req.Workload)); err != nil {
		return err
	}
	return run(tmpDir, "push", "origin", ref)
}

// copyFile is a local utility. The same function exists in internal/provisioner/local.go
// but that is a separate package and cannot be imported from here.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// embedHTTPSToken is shared with the provisioner git package — duplicated here to
// avoid a cross-package dependency on provisioner internals.
func embedHTTPSToken(rawURL, token string) string {
	const httpsPrefix = "https://"
	if len(rawURL) > len(httpsPrefix) && rawURL[:len(httpsPrefix)] == httpsPrefix {
		return httpsPrefix + token + "@" + rawURL[len(httpsPrefix):]
	}
	return rawURL
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/deployer/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/deployer/git.go
git commit -m "feat: add git deployer (clone, copy manifest, commit, push)"
```

---

## Task 14: Deployer — webhook

**Files:**
- Create: `internal/deployer/webhook.go`

Makes an HTTP request to the configured URL with configurable method, headers, body, and timeout. All string fields support `$env{VAR}` and `${context}` interpolation.

- [ ] **Step 1: Write `internal/deployer/webhook.go`**

```go
package deployer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/score-spec/score-orchestrator/internal/config"
	"github.com/score-spec/score-orchestrator/internal/interpolate"
)

// WebhookDeployer fires an HTTP request to a configurable endpoint.
type WebhookDeployer struct {
	name string
	cfg  config.WebhookConfig
}

func NewWebhookDeployer(name string, cfg config.WebhookConfig) *WebhookDeployer {
	return &WebhookDeployer{name: name, cfg: cfg}
}

func (d *WebhookDeployer) Name() string { return d.name }

func (d *WebhookDeployer) Deploy(ctx context.Context, req DeployRequest) error {
	vars := map[string]string{"org": req.Org, "env": req.Env, "workload": req.Workload}

	url := interpolate.Expand(d.cfg.URL, vars)
	method := d.cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	body := interpolate.Expand(d.cfg.Body, vars)

	timeout := time.Duration(d.cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	for k, v := range d.cfg.Headers {
		httpReq.Header.Set(k, interpolate.Expand(v, vars))
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/deployer/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/deployer/webhook.go
git commit -m "feat: add webhook deployer with timeout and header interpolation"
```

---

## Task 15: Pipeline — request types and input validation

**Files:**
- Create: `internal/pipeline/request.go`
- Create: `internal/pipeline/validate.go`

- [ ] **Step 1: Write `internal/pipeline/request.go`**

```go
package pipeline

import "time"

// DeployRequest is the input to the deploy pipeline.
type DeployRequest struct {
	Org       string
	Env       string
	Workload  string
	ScoreYAML []byte // raw score.yaml content
}

// DeployResult is returned on pipeline success.
type DeployResult struct {
	Org          string    `json:"org"`
	Env          string    `json:"env"`
	Workload     string    `json:"workload"`
	Status       string    `json:"status"` // "success" | "deploy_failed"
	DeployersRun []string  `json:"deployers_run"`
	DeployedAt   time.Time `json:"deployed_at"`
}
```

- [ ] **Step 2: Write `internal/pipeline/validate.go`**

```go
package pipeline

import (
	"fmt"
	"regexp"

	oerr "github.com/score-spec/score-orchestrator/internal/errors"
	"gopkg.in/yaml.v3"
)

var identityPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateInput checks that org, env, workload match the safe identity pattern
// and that scoreYAML is non-empty and parseable YAML.
func ValidateInput(org, env, workload string, scoreYAML []byte) *oerr.OrchestratorError {
	for field, val := range map[string]string{"org": org, "env": env, "workload": workload} {
		if val == "" {
			return oerr.New(oerr.CodeInvalidInput, fmt.Sprintf("%s is required", field), "", "validate", 400)
		}
		if !identityPattern.MatchString(val) {
			return oerr.New(oerr.CodeInvalidInput,
				fmt.Sprintf("%s must match [a-zA-Z0-9_-]+", field),
				fmt.Sprintf("got: %q", val), "validate", 400)
		}
	}
	if len(scoreYAML) == 0 {
		return oerr.New(oerr.CodeInvalidScore, "score.yaml is empty", "", "validate", 400)
	}
	var parsed any
	if err := yaml.Unmarshal(scoreYAML, &parsed); err != nil {
		return oerr.New(oerr.CodeInvalidScore, "score.yaml is not valid YAML", err.Error(), "validate", 400)
	}
	return nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/pipeline/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/pipeline/request.go internal/pipeline/validate.go
git commit -m "feat: add pipeline request types and input validation"
```

---

## Task 16: Pipeline — core Deploy function

**Files:**
- Create: `internal/pipeline/pipeline.go`

This is the heart of the orchestrator. Implements all 12 pipeline steps from the spec.

- [ ] **Step 1: Write `internal/pipeline/pipeline.go`**

```go
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	oerr "github.com/score-spec/score-orchestrator/internal/errors"
	"github.com/score-spec/score-orchestrator/internal/deployer"
	"github.com/score-spec/score-orchestrator/internal/provisioner"
	"github.com/score-spec/score-orchestrator/internal/score"
	"github.com/score-spec/score-orchestrator/internal/state"
)

// Runner holds the wired-up dependencies for the deploy pipeline.
type Runner struct {
	Backend    state.StateBackend
	Provisioner provisioner.ProvisionerSource
	Deployers  []deployer.Deployer
}

// Run executes the 12-step deploy pipeline for the given request.
// Returns a DeployResult on success, or an *oerr.OrchestratorError on failure.
func (r *Runner) Run(ctx context.Context, req DeployRequest) (*DeployResult, *oerr.OrchestratorError) {
	// Step 1: Validate input.
	if oe := ValidateInput(req.Org, req.Env, req.Workload, req.ScoreYAML); oe != nil {
		return nil, oe
	}

	// Step 2: Pull state.yaml from backend.
	stateYAML, etag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to pull state from backend", err.Error(), "state", 500)
	}
	isFresh := stateYAML == nil

	// Step 3: Sync provisioners into a temp provisioner dir (we'll copy to workDir later).
	provTmp, err := os.MkdirTemp("", "score-provisioners-*")
	if err != nil {
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to create provisioner temp dir", err.Error(), "provisioner", 500)
	}
	defer os.RemoveAll(provTmp)

	if syncErr := r.Provisioner.Sync(ctx, provTmp); syncErr != nil {
		return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to sync provisioners", syncErr.Error(), "provisioner", 500)
	}

	// Step 4: Create temp working directory.
	workDir, err := os.MkdirTemp("", "score-workdir-*")
	if err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to create work dir", err.Error(), "generate", 500)
	}
	defer os.RemoveAll(workDir)

	scoreDotK8s := filepath.Join(workDir, ".score-k8s")
	if err := os.MkdirAll(scoreDotK8s, 0755); err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to create .score-k8s dir", err.Error(), "generate", 500)
	}

	// Write score.yaml.
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
	for _, pf := range provFiles {
		data, err := os.ReadFile(pf)
		if err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to read provisioner file", err.Error(), "provisioner", 500)
		}
		if err := os.WriteFile(filepath.Join(scoreDotK8s, filepath.Base(pf)), data, 0644); err != nil {
			return nil, oerr.New(oerr.CodeProvisionerSyncFailed, "failed to write provisioner file", err.Error(), "provisioner", 500)
		}
	}

	// Step 5: Run score-k8s init (fresh workloads only).
	if isFresh {
		if err := score.RunInit(ctx, workDir); err != nil {
			return nil, oerr.New(oerr.CodeScoreGenerateFailed, "score-k8s init failed", err.Error(), "generate", 500)
		}
	}

	// Step 6: Run score-k8s generate.
	manifestsFile := filepath.Join(workDir, "manifests.yaml")
	stderr, genErr := score.RunGenerate(ctx, workDir, manifestsFile)
	if genErr != nil {
		meta := &state.DeployMeta{
			LastDeployedAt:   time.Now().UTC(),
			LastDeployStatus: "generate_failed",
			DeployersRun:     []string{},
		}
		_ = r.Backend.PushMeta(ctx, req.Org, req.Env, req.Workload, meta)
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "score-k8s generate failed", fmt.Sprintf("%s\n%s", genErr.Error(), stderr), "generate", 500)
	}

	// Step 7: Check ETag conflict.
	currentStateYAML, currentETag, err := r.Backend.PullState(ctx, req.Org, req.Env, req.Workload)
	if err != nil {
		return nil, oerr.New(oerr.CodeStatePullFailed, "failed to re-read state for ETag check", err.Error(), "state", 500)
	}
	_ = currentStateYAML
	if !isFresh && currentETag != etag {
		return nil, oerr.New(oerr.CodeStateConflict, "state was modified by a concurrent deployment, retry", "", "state", 409)
	}

	// Read updated state.yaml from workDir (score-k8s generate may have updated it).
	updatedStateYAML, err := os.ReadFile(filepath.Join(scoreDotK8s, "state.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to read updated state.yaml", err.Error(), "generate", 500)
	}
	manifestsYAML, err := os.ReadFile(manifestsFile)
	if err != nil {
		return nil, oerr.New(oerr.CodeScoreGenerateFailed, "failed to read manifests.yaml", err.Error(), "generate", 500)
	}

	// Step 8: Run deployers in sequence.
	deployReq := deployer.DeployRequest{
		Org:           req.Org,
		Env:           req.Env,
		Workload:      req.Workload,
		ManifestsPath: manifestsFile,
	}
	var deployErr error
	var deployersRun []string
	for _, d := range r.Deployers {
		deployersRun = append(deployersRun, d.Name())
		if err := d.Deploy(ctx, deployReq); err != nil {
			deployErr = fmt.Errorf("deployer %q failed: %w", d.Name(), err)
			break
		}
	}

	deployStatus := "success"
	if deployErr != nil {
		deployStatus = "deploy_failed"
	}

	// Step 9: Push state.yaml with ETag precondition.
	if updatedStateYAML != nil {
		if err := r.Backend.PushState(ctx, req.Org, req.Env, req.Workload, updatedStateYAML, etag); err != nil {
			if _, ok := err.(*state.StateConflictError); ok {
				return nil, oerr.New(oerr.CodeStateConflict, err.Error(), "", "state", 409)
			}
			return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push state.yaml", err.Error(), "state", 500)
		}
	}

	// Step 10: Push artifacts.
	meta := &state.DeployMeta{
		LastDeployedAt:   time.Now().UTC(),
		LastDeployStatus: deployStatus,
		DeployersRun:     deployersRun,
	}
	if err := r.Backend.PushArtifacts(ctx, req.Org, req.Env, req.Workload, req.ScoreYAML, manifestsYAML, meta); err != nil {
		return nil, oerr.New(oerr.CodeStatePushFailed, "failed to push artifacts", err.Error(), "state", 500)
	}

	// Steps 11–12: workDir cleanup is deferred above; return result.
	result := &DeployResult{
		Org:          req.Org,
		Env:          req.Env,
		Workload:     req.Workload,
		Status:       deployStatus,
		DeployersRun: deployersRun,
		DeployedAt:   meta.LastDeployedAt,
	}
	if deployErr != nil {
		return result, oerr.New(oerr.CodeDeployFailed, deployErr.Error(), "", "deploy", 500)
	}
	return result, nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/pipeline/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/pipeline/pipeline.go
git commit -m "feat: add core deploy pipeline (12-step orchestration)"
```

---

## Task 17: API — HTTP server, handlers, and error helpers

**Files:**
- Create: `api/errors.go`
- Create: `api/handlers.go`
- Create: `api/server.go`

The status endpoint derives `overall_status` from stored `score.yaml`, `state.yaml`, and `deploy_meta.json` by parsing resource outputs.

- [ ] **Step 1: Write `api/errors.go`**

```go
package api

import (
	"encoding/json"
	"net/http"

	oerr "github.com/score-spec/score-orchestrator/internal/errors"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	Stage   string `json:"stage,omitempty"`
}

func writeError(w http.ResponseWriter, oe *oerr.OrchestratorError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(oe.HTTPStatus)
	json.NewEncoder(w).Encode(errorResponse{
		Error: errorBody{
			Code:    oe.Code,
			Message: oe.Message,
			Detail:  oe.Detail,
			Stage:   oe.Stage,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 2: Write `api/handlers.go`**

```go
package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	oerr "github.com/score-spec/score-orchestrator/internal/errors"
	"github.com/score-spec/score-orchestrator/internal/pipeline"
	"github.com/score-spec/score-orchestrator/internal/state"
	"gopkg.in/yaml.v3"
)

// handleDeploy handles POST /api/v1/deploy.
// Accepts multipart/form-data or application/json (score field as base64).
func handleDeploy(runner *pipeline.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req pipeline.DeployRequest

		ct := r.Header.Get("Content-Type")
		switch {
		case len(ct) >= 19 && ct[:19] == "multipart/form-data":
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid multipart form", err.Error(), "validate", 400))
				return
			}
			req.Org = r.FormValue("org")
			req.Env = r.FormValue("env")
			req.Workload = r.FormValue("workload")
			f, _, err := r.FormFile("score")
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "score file missing from form", err.Error(), "validate", 400))
				return
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "failed to read score file", err.Error(), "validate", 400))
				return
			}
			req.ScoreYAML = data
		default:
			// JSON body with base64-encoded score field.
			var body struct {
				Org      string `json:"org"`
				Env      string `json:"env"`
				Workload string `json:"workload"`
				Score    string `json:"score"` // base64 encoded
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid JSON body", err.Error(), "validate", 400))
				return
			}
			req.Org = body.Org
			req.Env = body.Env
			req.Workload = body.Workload
			decoded, err := base64.StdEncoding.DecodeString(body.Score)
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "score field must be base64-encoded", err.Error(), "validate", 400))
				return
			}
			req.ScoreYAML = decoded
		}

		result, oe := runner.Run(r.Context(), req)
		if oe != nil && result == nil {
			writeError(w, oe)
			return
		}
		// If there's a deploy error but we have a result, return 500 with result embedded.
		if oe != nil {
			writeError(w, oe)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// handleStatus handles GET /api/v1/workloads/{org}/{env}/{workload}/status.
func handleStatus(backend state.StateBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := chi.URLParam(r, "org")
		env := chi.URLParam(r, "env")
		workload := chi.URLParam(r, "workload")

		files, err := backend.GetStatus(r.Context(), org, env, workload)
		if err != nil {
			writeError(w, oerr.New(oerr.CodeStatePullFailed, "failed to get workload status", err.Error(), "state", 500))
			return
		}

		resp := buildStatusResponse(org, env, workload, files)
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleManifest handles GET /api/v1/workloads/{org}/{env}/{workload}/manifest.
func handleManifest(backend state.StateBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := chi.URLParam(r, "org")
		env := chi.URLParam(r, "env")
		workload := chi.URLParam(r, "workload")

		data, err := backend.GetManifest(r.Context(), org, env, workload)
		if err != nil {
			writeError(w, oerr.New(oerr.CodeStatePullFailed, "failed to get manifest", err.Error(), "state", 500))
			return
		}
		if data == nil {
			writeError(w, oerr.New(oerr.CodeWorkloadNotFound, "workload not found", "", "", 404))
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}
}

func handleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// --- Status derivation ---

type resourceDetail struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Class   string   `json:"class"`
	Status  string   `json:"status"`
	Outputs []string `json:"outputs"`
}

type resourceSummary struct {
	Total       int `json:"total"`
	Provisioned int `json:"provisioned"`
	Pending     int `json:"pending"`
	Failed      int `json:"failed"`
}

type statusResponse struct {
	Org              string          `json:"org"`
	Env              string          `json:"env"`
	Workload         string          `json:"workload"`
	OverallStatus    string          `json:"overall_status"`
	Resources        resourceSummary `json:"resources"`
	ResourceDetail   []resourceDetail `json:"resource_detail"`
	LastDeployedAt   *string         `json:"last_deployed_at,omitempty"`
	LastDeployStatus *string         `json:"last_deploy_status,omitempty"`
}

// scoreResources parses the resources section of a score.yaml.
// Returns a slice of {name, type, class} maps.
func scoreResources(scoreYAML []byte) []map[string]string {
	var doc struct {
		Resources map[string]struct {
			Type  string `yaml:"type"`
			Class string `yaml:"class"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal(scoreYAML, &doc); err != nil {
		return nil
	}
	var out []map[string]string
	for name, r := range doc.Resources {
		out = append(out, map[string]string{
			"name":  name,
			"type":  r.Type,
			"class": r.Class,
		})
	}
	return out
}

// stateOutputs parses state.yaml and returns a map of resource UID → output keys.
// score-k8s state.yaml structure: {resources: {uid: {outputs: {...}}}}
func stateOutputs(stateYAML []byte) map[string][]string {
	var doc struct {
		Resources map[string]struct {
			Outputs map[string]any `yaml:"outputs"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal(stateYAML, &doc); err != nil {
		return nil
	}
	result := make(map[string][]string)
	for uid, r := range doc.Resources {
		var keys []string
		for k := range r.Outputs {
			keys = append(keys, k)
		}
		result[uid] = keys
	}
	return result
}

func buildStatusResponse(org, env, workload string, files *state.WorkloadFiles) statusResponse {
	resp := statusResponse{Org: org, Env: env, Workload: workload}

	// No state at all.
	if files.ScoreYAML == nil && files.StateYAML == nil && files.DeployMeta == nil {
		resp.OverallStatus = "unknown"
		return resp
	}

	if files.DeployMeta != nil {
		t := files.DeployMeta.LastDeployedAt.Format("2006-01-02T15:04:05Z")
		s := files.DeployMeta.LastDeployStatus
		resp.LastDeployedAt = &t
		resp.LastDeployStatus = &s
	}

	// Derive per-resource status.
	resources := scoreResources(files.ScoreYAML)
	outputs := stateOutputs(files.StateYAML)

	for _, r := range resources {
		name := r["name"]
		rtype := r["type"]
		class := r["class"]
		if class == "" {
			class = "default"
		}

		// Find the resource in state outputs by matching name prefix in the UID key.
		// score-k8s UID format is typically "{type}.{class}#{id}" or "{name}".
		var outputKeys []string
		var rStatus string

		for uid, keys := range outputs {
			if uid == name || len(uid) > len(name) && uid[:len(name)] == name {
				outputKeys = keys
				break
			}
		}

		if len(outputKeys) > 0 {
			rStatus = "provisioned"
			resp.Resources.Provisioned++
		} else {
			// NOTE: Resource-level "failed" status (spec: "present in state.yaml with an
			// error marker") is not yet implemented because the score-k8s error marker
			// format in state.yaml is not publicly documented. All non-provisioned
			// resources are treated as "pending" until the marker format is confirmed.
			rStatus = "pending"
			resp.Resources.Pending++
		}
		resp.Resources.Total++
		resp.ResourceDetail = append(resp.ResourceDetail, resourceDetail{
			Name:    name,
			Type:    rtype,
			Class:   class,
			Status:  rStatus,
			Outputs: outputKeys,
		})
	}

	// Compute overall_status using the spec algorithm.
	// Note: spec step 3 (any resource "failed") is currently unreachable because
	// resource-level error marker detection is deferred (see comment above).
	lastStatus := ""
	if files.DeployMeta != nil {
		lastStatus = files.DeployMeta.LastDeployStatus
	}

	switch {
	case lastStatus == "generate_failed":
		resp.OverallStatus = "failed"
	case resp.Resources.Provisioned == resp.Resources.Total && resp.Resources.Total > 0 && lastStatus == "success":
		resp.OverallStatus = "ready"
	case resp.Resources.Provisioned > 0 && resp.Resources.Pending > 0:
		resp.OverallStatus = "partial"
	default:
		resp.OverallStatus = "failed"
	}
	return resp
}
```

- [ ] **Step 3: Write `api/server.go`**

```go
package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/score-spec/score-orchestrator/internal/pipeline"
	"github.com/score-spec/score-orchestrator/internal/state"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	runner  *pipeline.Runner
	backend state.StateBackend
	port    int
}

func NewServer(runner *pipeline.Runner, backend state.StateBackend, port int) *Server {
	return &Server{runner: runner, backend: backend, port: port}
}

func (s *Server) Start() error {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/api/v1/deploy", handleDeploy(s.runner))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/status", handleStatus(s.backend))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/manifest", handleManifest(s.backend))
	r.Get("/healthz", handleHealthz())

	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, r)
}
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./api/...
```

- [ ] **Step 5: Commit**

```bash
git add api/errors.go api/handlers.go api/server.go
git commit -m "feat: add HTTP server with deploy, status, manifest, and healthz endpoints"
```

---

## Task 18: CLI commands and main.go wiring

**Files:**
- Create: `cmd/root.go`
- Create: `cmd/deploy.go`
- Create: `cmd/server.go`
- Create: `cmd/wire.go`
- Modify: `main.go` (already created, no changes needed)

The `root.go` loads the config. `deploy.go` and `server.go` build the wired `pipeline.Runner` from config and call the pipeline or start the server.

- [ ] **Step 1: Write `cmd/root.go`**

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/score-spec/score-orchestrator/internal/config"
)

var (
	cfgFile string
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "score-orchestrator",
	Short: "Thin orchestrator for score-k8s: state backends, provisioner sync, and deployment",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config %q: %w", cfgFile, err)
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "orchestrator.yaml", "path to orchestrator.yaml")
}
```

- [ ] **Step 2: Write `cmd/deploy.go`**

```go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/score-spec/score-orchestrator/internal/pipeline"
)

var (
	deployScoreFile string
	deployOrg       string
	deployEnv       string
	deployWorkload  string
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Run the deploy pipeline for a workload",
	RunE: func(cmd *cobra.Command, args []string) error {
		scoreYAML, err := os.ReadFile(deployScoreFile)
		if err != nil {
			return fmt.Errorf("read score file: %w", err)
		}

		runner, err := buildRunner(cfg)
		if err != nil {
			return err
		}

		result, oe := runner.Run(context.Background(), pipeline.DeployRequest{
			Org:       deployOrg,
			Env:       deployEnv,
			Workload:  deployWorkload,
			ScoreYAML: scoreYAML,
		})
		if oe != nil && result == nil {
			fmt.Fprintf(os.Stderr, "error [%s]: %s\n", oe.Code, oe.Message)
			if oe.Detail != "" {
				fmt.Fprintf(os.Stderr, "detail: %s\n", oe.Detail)
			}
			os.Exit(1)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		if oe != nil {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
	deployCmd.Flags().StringVar(&deployScoreFile, "score", "score.yaml", "path to score.yaml")
	deployCmd.Flags().StringVar(&deployOrg, "org", "", "organisation identifier")
	deployCmd.Flags().StringVar(&deployEnv, "env", "", "environment (e.g. prod, staging)")
	deployCmd.Flags().StringVar(&deployWorkload, "workload", "", "workload name")
	deployCmd.MarkFlagRequired("org")
	deployCmd.MarkFlagRequired("env")
	deployCmd.MarkFlagRequired("workload")
}
```

- [ ] **Step 3: Write `cmd/server.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/score-spec/score-orchestrator/api"
)

var serverPort int

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP REST server",
	RunE: func(cmd *cobra.Command, args []string) error {
		runner, err := buildRunner(cfg)
		if err != nil {
			return err
		}
		// Reuse the backend already wired into the runner — avoids creating a second client.
		srv := api.NewServer(runner, runner.Backend, serverPort)
		fmt.Printf("score-orchestrator server listening on :%d\n", serverPort)
		return srv.Start()
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().IntVar(&serverPort, "port", 8080, "HTTP server port")
}
```

- [ ] **Step 4: Write `cmd/wire.go` — shared wiring helpers for building Runner and Backend from config**

```go
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
```

- [ ] **Step 5: Verify full build**

```bash
go build ./...
```

Expected: exits 0, no output.

- [ ] **Step 6: Commit**

```bash
git add cmd/root.go cmd/deploy.go cmd/server.go cmd/wire.go
git commit -m "feat: add CLI commands (deploy, server) and dependency wiring"
```

---

## Task 19: Example orchestrator.yaml

**Files:**
- Create: `orchestrator.yaml`

- [ ] **Step 1: Write `orchestrator.yaml`**

```yaml
# orchestrator.yaml — platform-level config for score-orchestrator
# All credentials must be provided via environment variables, never stored here.

state:
  backend: file          # "file" | "s3"
  file:
    base_path: ./state   # state stored at ./state/{org}/{env}/{workload}/
  s3:
    bucket: my-platform-state
    prefix: score-state
    region: us-east-1
    # Credentials: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, or instance role

provisioners:
  source: local          # "local" | "git"
  local:
    path: ./provisioners # *.provisioners.yaml files copied from here
  git:
    url: git@github.com:my-org/score-provisioners.git
    ref: main            # branch, tag, or commit SHA
    path: k8s/           # subdirectory within the repo
    auth:
      type: ssh          # "ssh" | "https"
      ssh_key_file: ~/.ssh/id_ed25519
      # https: set token_env to the env var holding the token
      # token_env: PROVISIONER_GIT_TOKEN

deployers:
  - name: k8s
    type: kubectl
    kubectl:
      kubeconfig: ~/.kube/config
      namespace: "${env}"           # resolved from deploy-time context
      context: "${org}-${env}"
  - name: gitops
    type: git
    git:
      url: https://github.com/my-org/gitops-repo.git
      ref: main
      path: clusters/prod/manifests/
      auth:
        type: https
        token_env: GITOPS_TOKEN     # export GITOPS_TOKEN=<your-token>
  - name: cd-trigger
    type: webhook
    webhook:
      url: https://argocd.internal/api/v1/applications/${workload}/sync
      method: POST
      timeout_seconds: 30
      headers:
        Authorization: "Bearer $env{ARGOCD_TOKEN}"
      body: |
        {"revision": "HEAD"}
```

- [ ] **Step 2: Final full build check**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add orchestrator.yaml
git commit -m "chore: add example orchestrator.yaml with all deployer and backend options"
```

---

## Build Verification

After all tasks are complete, verify the binary builds and the CLI is wired:

```bash
go build -o score-orchestrator .
./score-orchestrator --help
./score-orchestrator deploy --help
./score-orchestrator server --help
```

Expected output for `--help`: cobra usage text showing the three subcommands and global `--config` flag.
