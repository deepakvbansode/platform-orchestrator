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
