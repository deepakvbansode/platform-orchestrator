# Score Orchestrator — Design Spec

**Date:** 2026-03-23
**Status:** Approved

---

## Problem Statement

`score-k8s` generates Kubernetes manifests from Score workload specs but has three platform-level gaps:

1. **No deployment** — it only generates manifests; nothing applies them.
2. **No concurrent safety** — state is a single file per project; concurrent runs overwrite each other.
3. **No provisioner management** — provisioners must be manually placed in `.score-k8s/`; there is no way to pull them from a central source.

This orchestrator is a thin Go service that wraps `score-k8s` to close all three gaps.

---

## Approach

**Pipeline-per-command (Approach A):** The orchestrator shells out to `score-k8s` for manifest generation and acts as a coordinator for state, provisioners, and deployment. Each concern is a Go `interface` with pluggable implementations. A single binary exposes both a CLI and an embedded HTTP REST server.

---

## Architecture

### Package Structure

```
score-orchestrator/
├── cmd/
│   ├── root.go          # cobra root, reads orchestrator.yaml
│   ├── deploy.go        # CLI: score-orchestrator deploy
│   └── server.go        # CLI: score-orchestrator server
├── internal/
│   ├── config/          # orchestrator.yaml loader + types
│   ├── pipeline/        # core deploy pipeline
│   ├── state/           # StateBackend interface + file & S3 implementations
│   ├── provisioner/     # ProvisionerSource interface + git & local implementations
│   ├── deployer/        # Deployer interface + kubectl, git-push, webhook implementations
│   └── score/           # thin wrapper: shells out to score-k8s
├── api/
│   └── server.go        # HTTP REST server, routes delegate to pipeline
├── orchestrator.yaml    # platform config (not per-project)
└── main.go
```

### Key Interfaces

```go
type StateBackend interface {
    Pull(ctx context.Context, org, env, workload string) (*WorkloadState, error)
    Push(ctx context.Context, org, env, workload string, state *WorkloadState, etag string) error
}

type ProvisionerSource interface {
    Sync(ctx context.Context, destDir string) error
}

type Deployer interface {
    Name() string
    Deploy(ctx context.Context, req DeployRequest) error
}
```

---

## Invocation Model

The orchestrator is a **platform-level service**, not a per-project tool. Score files are supplied at invocation time alongside an identity context: `org`, `env`, `workload`.

`org`, `env`, and `workload` are validated at the start of every request to match the pattern `[a-zA-Z0-9_-]+`. Any value that does not match returns `INVALID_INPUT` immediately, preventing path traversal in the file backend or malformed S3 keys.

### CLI

```bash
score-orchestrator deploy \
  --score ./payment-service.yaml \
  --org my-org \
  --env prod \
  --workload payment-service
```

### HTTP

```
POST /api/v1/deploy
Content-Type: multipart/form-data

Fields:
  org:      my-org
  env:      prod
  workload: payment-service
  score:    <score.yaml file upload>
```

Or JSON with base64-encoded score content:

```json
{
  "org": "my-org",
  "env": "prod",
  "workload": "payment-service",
  "score": "<base64 encoded score.yaml>"
}
```

---

## State Layout

State is namespaced by `org/env/workload`. Four files are stored per workload:

```
{org}/{env}/{workload}/
  score.yaml        # last submitted score file
  state.yaml        # score-k8s provisioner state
  manifests.yaml    # last generated manifests
  deploy_meta.json  # orchestrator metadata (last_deployed_at, last_deploy_status)
```

`deploy_meta.json` is owned entirely by the orchestrator and is never passed to `score-k8s`. It carries fields that have no natural home in the score-k8s state format:

```json
{
  "last_deployed_at": "2026-03-23T10:15:00Z",
  "last_deploy_status": "success",
  "deployers_run": ["k8s", "cd-trigger"]
}
```

`last_deploy_status` values: `success`, `deploy_failed`, `generate_failed`.

`deployers_run` lists only the deployers that actually ran (were invoked). Deployers that were skipped due to an earlier failure are omitted. Example: if deployer `k8s` runs and succeeds but `cd-trigger` fails, `deployers_run` is `["k8s", "cd-trigger"]` and `last_deploy_status` is `deploy_failed`; if `k8s` fails, `deployers_run` is `["k8s"]`.

### File Backend

```
{base_path}/
  my-org/
    prod/
      payment-service/
        score.yaml
        state.yaml
        manifests.yaml
        deploy_meta.json
    staging/
      payment-service/
        ...
```

### S3 Backend

Keys follow the same pattern:
```
s3://{bucket}/{prefix}/{org}/{env}/{workload}/state.yaml
s3://{bucket}/{prefix}/{org}/{env}/{workload}/deploy_meta.json
...
```

### Optimistic Conflict Detection

ETag/checksum-based optimistic locking applies to `state.yaml` only — it is the authoritative record of provisioner state. `score.yaml`, `manifests.yaml`, and `deploy_meta.json` are last-write-wins; only one deploy should be in flight per workload at a time (enforced at the platform level), and `state.yaml` is the single file that must not be silently overwritten.

Before pushing state in step 9, the orchestrator compares the ETag captured at pull time (step 2) against the current ETag in the backend. If they differ (concurrent deploy), it returns `409 Conflict`:

```
"state was modified by a concurrent deployment, retry"
```

---

## `orchestrator.yaml` Config Schema

```yaml
state:
  backend: s3              # "file" | "s3"
  file:
    base_path: ./state
  s3:
    bucket: my-platform-state
    prefix: score-state    # optional key prefix
    region: us-east-1
    # credentials via AWS env vars or instance role

provisioners:
  source: git              # "git" | "local"
  git:
    url: git@github.com:my-org/score-provisioners.git
    ref: main              # branch, tag, or commit SHA
    path: k8s/             # subdirectory within the repo (optional)
    auth:
      type: ssh            # "ssh" | "https"
      ssh_key_file: ~/.ssh/id_ed25519
      # https: token read from env var PROVISIONER_GIT_TOKEN
  local:
    path: ./provisioners

deployers:
  - name: k8s
    type: kubectl
    kubectl:
      kubeconfig: ~/.kube/config
      namespace: "${env}"
      context: "${org}-${env}"
  - name: gitops
    type: git
    git:
      url: https://github.com/my-org/gitops-repo.git
      ref: main            # branch, tag, or commit SHA
      path: clusters/prod/manifests/
      auth:
        type: https
        token_env: GITOPS_TOKEN
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

**Variable interpolation rules:**
- `${org}`, `${env}`, `${workload}` — lowercase, resolve from request-time context. Request-context variable names are always lowercase.
- `$env{VAR_NAME}` — resolves from the process environment. Distinct syntax to avoid collision with request-context variables.
- Credentials are never stored in the file — always `$env{...}` references or file paths.
- Multiple deployers run sequentially in listed order. If one fails, subsequent deployers are skipped.

---

## Deploy Pipeline

```
1.  Validate input
      └─ org, env, workload must match [a-zA-Z0-9_-]+
      └─ score.yaml must be non-empty and parseable YAML
         → INVALID_INPUT on bad identity fields
         → INVALID_SCORE on bad score content
2.  Pull {org}/{env}/{workload}/state.yaml from backend; capture ETag
      └─ If not found: fresh workload, no state, ETag = ""
3.  Sync provisioners from source
      └─ git: shallow clone (--depth 1) configured ref into temp clone dir
              copy *.provisioners.yaml from configured subpath
      └─ local: copy *.provisioners.yaml from configured path
         → PROVISIONER_SYNC_FAILED on error
4.  Create temp working directory
      ├─ write score.yaml
      ├─ write .score-k8s/state.yaml  (from step 2; skip if fresh workload)
      └─ write .score-k8s/*.provisioners.yaml  (from step 3)
5.  Run: score-k8s init
      └─ Only if step 2 found no existing state (fresh workload)
      └─ Creates .score-k8s/zz-default.provisioners.yaml
      └─ Custom provisioners written in step 4 are not overwritten
         (score-k8s init does not touch existing *.provisioners.yaml files)
6.  Run: score-k8s generate --output manifests.yaml
      └─ On non-zero exit:
           write deploy_meta.json with last_deploy_status = "generate_failed"
           push deploy_meta.json to backend
           return SCORE_GENERATE_FAILED; do NOT push state.yaml
7.  Check ETag conflict: compare captured ETag (step 2) vs current backend ETag
         → STATE_CONFLICT (409) if they differ; stop
      Note: The ETag check is placed here (after generate, before push) deliberately.
      score-k8s generate is purely local (no network calls in the generate step itself);
      provisioner side effects happened during generate. Checking ETag late avoids an
      extra network round-trip before the fast local generate step, and the check still
      prevents any concurrent state overwrite before we actually push.
8.  Run deployers in sequence
      └─ On any failure: record last_deploy_status = "deploy_failed", continue to step 9
         (state is pushed because resources were provisioned by score-k8s generate;
          not pushing would cause re-provisioning on retry)
9.  Push state.yaml to backend (with ETag precondition)
         → STATE_PUSH_FAILED on error
10. Push score.yaml, manifests.yaml, deploy_meta.json to backend
      └─ deploy_meta.json records last_deployed_at, last_deploy_status, deployers_run
11. Cleanup temp directory
12. Return response — success or DEPLOY_FAILED depending on step 8 outcome
```

**State push on deployer failure (step 8):** State is always pushed after a successful `score-k8s generate`, even if a deployer fails. This is intentional: `score-k8s generate` has already provisioned resources (side effects in the cluster/cloud). Not saving state would cause those resources to be re-provisioned on the next attempt, leading to duplicates. The caller retries the deploy; on retry, generate uses the saved state and deployers are re-attempted.

---

## Provisioner Sync

- Provisioners are synced fresh on every deploy.
- **Git source:** shallow clone (`--depth 1`) the configured ref into a temp directory, copy all `*.provisioners.yaml` from the configured subdirectory. The clone directory is cleaned up after the copy.
- **Local source:** copy all `*.provisioners.yaml` from the configured local path.
- Pulled provisioner files are written to `.score-k8s/` in the temp working dir **before** `score-k8s init` runs (step 5). `score-k8s init` creates `zz-default.provisioners.yaml` but does not touch any other existing `*.provisioners.yaml` files, so there is no overwrite risk.
- `zz-default.provisioners.yaml` loads last (lexicographic order) and acts as the built-in fallback. Custom provisioners take precedence via first-match.

---

## REST API

### `POST /api/v1/deploy`

Trigger a full deploy pipeline for a workload.

**Request:** `multipart/form-data` or `application/json`

| Field | Type | Required | Description |
|---|---|---|---|
| `org` | string | yes | Organisation identifier (`[a-zA-Z0-9_-]+`) |
| `env` | string | yes | Environment (`[a-zA-Z0-9_-]+`) |
| `workload` | string | yes | Workload name (`[a-zA-Z0-9_-]+`) |
| `score` | file / base64 string | yes | score.yaml content |

**Response `200 OK`:**
```json
{
  "org": "my-org",
  "env": "prod",
  "workload": "payment-service",
  "status": "success",
  "deployers_run": ["k8s", "cd-trigger"],
  "deployed_at": "2026-03-23T10:15:00Z"
}
```

---

### `GET /api/v1/workloads/{org}/{env}/{workload}/status`

Return the provisioning status of a workload derived from its stored `score.yaml`, `state.yaml`, and `deploy_meta.json`.

**Behavior for missing workload:** If no stored state exists for the given `org/env/workload`, returns `200 OK` with `overall_status: "unknown"` (not a 404). This is intentional — callers treat "unknown" as "never deployed" rather than an error. Use `GET /manifest` if you need a hard 404 to detect non-existence.

**Resource status derivation:**
- **provisioned** — resource present in `state.yaml` with non-empty outputs
- **pending** — declared in `score.yaml` but absent or output-less in `state.yaml`
- **failed** — present in `state.yaml` with an error marker written by a previous failed run

**`overall_status` derivation algorithm:**
1. If no stored state → `unknown`
2. If `last_deploy_status` is `generate_failed` → `failed`
3. If any resource has status `failed` → `failed`
4. If all resources have status `provisioned` AND `last_deploy_status` is `success` → `ready`
5. If some resources are `provisioned` and others are `pending` → `partial`
6. Otherwise (all pending, or `last_deploy_status` is `deploy_failed`) → `failed`

**Response `200 OK`:**
```json
{
  "org": "my-org",
  "env": "prod",
  "workload": "payment-service",
  "overall_status": "partial",
  "resources": {
    "total": 3,
    "provisioned": 1,
    "pending": 2,
    "failed": 0
  },
  "resource_detail": [
    {
      "name": "db",
      "type": "postgres",
      "class": "default",
      "status": "provisioned",
      "outputs": ["host", "port", "username", "password", "database"]
    },
    {
      "name": "cache",
      "type": "redis",
      "class": "default",
      "status": "pending",
      "outputs": []
    },
    {
      "name": "app-dns",
      "type": "dns",
      "class": "default",
      "status": "pending",
      "outputs": []
    }
  ],
  "last_deployed_at": "2026-03-23T10:15:00Z",
  "last_deploy_status": "success"
}
```

**Overall status values:**

| Value | Meaning |
|---|---|
| `unknown` | No state found (workload never deployed) |
| `partial` | Some resources provisioned, others pending |
| `ready` | All resources provisioned, last deploy succeeded |
| `failed` | Last deploy run failed |

---

### `GET /api/v1/workloads/{org}/{env}/{workload}/manifest`

Retrieve the last generated `manifests.yaml` for a workload.

**Response `200 OK`:** `Content-Type: application/yaml` — raw manifest content.

**Response `404 Not Found`:** Standard error shape with code `WORKLOAD_NOT_FOUND`.

---

### `GET /healthz`

Liveness probe for server mode.

**Response `200 OK`:** `{"status": "ok"}`

---

## Error Response Shape

All error responses share this structure:

```json
{
  "error": {
    "code": "SCORE_GENERATE_FAILED",
    "message": "score-k8s generate exited with code 1",
    "detail": "<stderr output from score-k8s>",
    "stage": "generate"
  }
}
```

**Error codes:**

| Code | Stage | HTTP Status | Meaning |
|---|---|---|---|
| `INVALID_INPUT` | validate | 400 | Missing, malformed, or unsafe request fields |
| `INVALID_SCORE` | validate | 400 | score.yaml failed schema validation |
| `STATE_PULL_FAILED` | state | 500 | Could not read state from backend |
| `PROVISIONER_SYNC_FAILED` | provisioner | 500 | Could not clone/copy provisioners |
| `SCORE_GENERATE_FAILED` | generate | 500 | `score-k8s generate` exited non-zero |
| `STATE_CONFLICT` | state | 409 | Concurrent deploy modified state (retry) |
| `DEPLOY_FAILED` | deploy | 500 | A deployer returned an error |
| `STATE_PUSH_FAILED` | state | 500 | Could not write state to backend |
| `WORKLOAD_NOT_FOUND` | — | 404 | No stored state for the requested workload |

---

## Technology Choices

| Concern | Choice | Reason |
|---|---|---|
| Language | Go | Native fit for CLI + server, small binary |
| CLI framework | [cobra](https://github.com/spf13/cobra) | Standard Go CLI framework |
| HTTP router | [chi](https://github.com/go-chi/chi) | Lightweight, idiomatic, no framework lock-in |
| Config parsing | [viper](https://github.com/spf13/viper) + [mapstructure](https://github.com/mitchellh/mapstructure) | YAML config with env var override |
| S3 client | [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2) | Official AWS SDK |
| Git operations | Shell out to `git` CLI | Avoids libgit2 CGO dependency; SSH key auth is trivial |
| Manifest apply | Shell out to `kubectl apply` | No client-go dependency |
| score-k8s | Shell out to `score-k8s` binary | Keeps orchestrator thin, decoupled from score internals |
| Testing | None | Per project requirement |
