package action

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ActionStateBackend persists action execution state.
type ActionStateBackend interface {
	PutState(ctx context.Context, name string, state *ActionState) error
	GetState(ctx context.Context, name string) (*ActionState, error)
}

// FileActionStateBackend stores action state as JSON files under
// {BasePath}/actions/{name}/state.json.
type FileActionStateBackend struct {
	BasePath string
}

// NewFileActionStateBackend creates a file-backed action state store.
func NewFileActionStateBackend(basePath string) *FileActionStateBackend {
	return &FileActionStateBackend{BasePath: basePath}
}

func (b *FileActionStateBackend) stateFile(name string) string {
	return filepath.Join(b.BasePath, "actions", name, "state.json")
}

// PutState writes the action state to disk, setting UpdatedAt to now.
func (b *FileActionStateBackend) PutState(_ context.Context, name string, s *ActionState) error {
	path := b.stateFile(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// GetState reads the action state from disk. Returns nil, nil when not found.
func (b *FileActionStateBackend) GetState(_ context.Context, name string) (*ActionState, error) {
	path := b.stateFile(name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s ActionState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
