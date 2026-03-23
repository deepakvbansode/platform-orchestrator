# Contributing

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | ≥ 1.24 | https://go.dev/dl/ |
| score-k8s | latest | `brew install score-spec/tap/score-k8s` or https://github.com/score-spec/score-k8s/releases |
| kubectl | any | https://kubernetes.io/docs/tasks/tools/ |
| git | any | pre-installed on most systems |

Optional (for S3 state backend): AWS credentials via environment variables or instance role.

## Clone and build

```bash
git clone https://github.com/deepakvbansode/platform-orchestrator.git
cd platform-orchestrator
go mod download
make build
```

Verify:

```bash
./score-orchestrator --help
```

## Project layout

```
main.go                     entry point
cmd/                        cobra CLI — deploy and server subcommands
internal/
  config/                   orchestrator.yaml loader
  interpolate/              ${org/env/workload} and $env{VAR} expansion
  errors/                   shared error types and codes
  state/                    StateBackend interface — file and S3 implementations
  provisioner/              ProvisionerSource interface — local and git implementations
  score/                    thin wrapper around the score-k8s binary
  deployer/                 Deployer interface — kubectl, git-push, webhook
  pipeline/                 12-step deploy pipeline (validate → generate → deploy → push state)
api/                        chi HTTP server — REST endpoints
orchestrator.yaml           example platform config
```

## Configuration

Copy the example config and adjust for your environment:

```bash
cp orchestrator.yaml orchestrator.local.yaml
```

The minimum config for local development uses the file state backend and a local provisioners directory:

```yaml
state:
  backend: file
  file:
    base_path: ./state

provisioners:
  source: local
  local:
    path: ./provisioners

deployers:
  - name: k8s
    type: kubectl
    kubectl:
      kubeconfig: ~/.kube/config
      namespace: "${env}"
```

Create the provisioners directory (can be empty to start):

```bash
mkdir -p provisioners
```

## Running locally

### HTTP server

```bash
make run-server CONFIG=orchestrator.local.yaml
# or
./score-orchestrator server --config orchestrator.local.yaml --port 8080
```

The server starts on `:8080`. Check it is healthy:

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

### CLI deploy

```bash
make run-deploy \
  CONFIG=orchestrator.local.yaml \
  ORG=my-org \
  ENV=dev \
  WORKLOAD=my-service \
  SCORE=./score.yaml
# or
./score-orchestrator deploy \
  --config orchestrator.local.yaml \
  --org my-org \
  --env dev \
  --workload my-service \
  --score ./score.yaml
```

### HTTP deploy

```bash
# multipart
curl -X POST http://localhost:8080/api/v1/deploy \
  -F org=my-org \
  -F env=dev \
  -F workload=my-service \
  -F score=@./score.yaml

# JSON with base64-encoded score file
curl -X POST http://localhost:8080/api/v1/deploy \
  -H "Content-Type: application/json" \
  -d "{\"org\":\"my-org\",\"env\":\"dev\",\"workload\":\"my-service\",\"score\":\"$(base64 < ./score.yaml)\"}"
```

### Workload status and manifest

```bash
curl http://localhost:8080/api/v1/workloads/my-org/dev/my-service/status
curl http://localhost:8080/api/v1/workloads/my-org/dev/my-service/manifest
```

## State backends

### File (default)

State is stored under `base_path/{org}/{env}/{workload}/`. No extra setup needed. Files written:

```
state/
  my-org/
    dev/
      my-service/
        score.yaml
        state.yaml
        manifests.yaml
        deploy_meta.json
```

### S3

Set the following environment variables before starting:

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1   # or set region in orchestrator.yaml
```

Then update `orchestrator.yaml`:

```yaml
state:
  backend: s3
  s3:
    bucket: my-platform-state
    prefix: score-state
    region: us-east-1
```

## Provisioner sources

### Local

Place `*.provisioners.yaml` files in the configured directory. They are copied into each deploy's temp working dir before `score-k8s generate` runs.

### Git

The orchestrator does a shallow clone (`--depth 1`) of the configured ref on every deploy. SSH auth example:

```yaml
provisioners:
  source: git
  git:
    url: git@github.com:my-org/score-provisioners.git
    ref: main
    path: k8s/
    auth:
      type: ssh
      ssh_key_file: ~/.ssh/id_ed25519
```

HTTPS auth reads the token from an environment variable:

```yaml
    auth:
      type: https
      token_env: PROVISIONER_GIT_TOKEN
```

## Deployer types

| Type | Config key | What it does |
|---|---|---|
| `kubectl` | `kubectl` | Runs `kubectl apply -f manifests.yaml` against the configured cluster context |
| `git` | `git` | Clones a gitops repo, writes the manifest, commits and pushes |
| `webhook` | `webhook` | HTTP POST to a URL (e.g. ArgoCD sync) with configurable headers and body |

Multiple deployers run sequentially. If one fails, subsequent deployers are skipped.

## Variable interpolation

Two syntaxes are supported in `orchestrator.yaml` values:

| Syntax | Resolves to |
|---|---|
| `${org}`, `${env}`, `${workload}` | Request-time identity (lowercase only) |
| `$env{VAR_NAME}` | Process environment variable |

Example:

```yaml
namespace: "${env}"
context: "${org}-${env}"
headers:
  Authorization: "Bearer $env{ARGOCD_TOKEN}"
```

## Make targets

```
make build                    compile the binary
make run-server               build and start HTTP server on :8080
make run-server PORT=9090     start on a custom port
make run-deploy               deploy with default vars
make fmt                      format Go source
make vet                      run go vet
make tidy                     tidy go.mod / go.sum
make clean                    remove compiled binary
```

## Making changes

```bash
# after editing
make fmt
make vet
make build
```

Commit messages follow the conventional commits format: `feat:`, `fix:`, `chore:`, `docs:`.
