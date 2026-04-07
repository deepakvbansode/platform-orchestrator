package action

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ActionProvisioner represents a single action provisioner entry in a
// *.provisioners.yaml file. Entries without a uri field are score provisioners
// (consumed by score-k8s) and are ignored by the action runner.
type ActionProvisioner struct {
	URI          string   `yaml:"uri"`
	ResourceType string   `yaml:"resourceType"`
	Class        string   `yaml:"class"`
	ID           string   `yaml:"id"`
	Description  string   `yaml:"description"`
	Args         []string `yaml:"args"`
	// Init is a Go template block executed for validation before manifest generation.
	// Use {{ fail "msg" }} to reject invalid inputs.
	Init string `yaml:"init"`
	// State is a Go template block that renders a YAML map; its keys become
	// available as .State.keyName in the Manifests template.
	State string `yaml:"state"`
	// Manifests is a Go template block that renders a YAML list of K8s resources.
	Manifests string `yaml:"manifests"`
}

// URIScheme returns the scheme part of the provisioner URI (e.g., "template", "cmd").
func (p *ActionProvisioner) URIScheme() string {
	idx := strings.Index(p.URI, "://")
	if idx < 0 {
		return ""
	}
	return p.URI[:idx]
}

// URIPath returns the path component after the "://" separator.
func (p *ActionProvisioner) URIPath() string {
	idx := strings.Index(p.URI, "://")
	if idx < 0 {
		return p.URI
	}
	return p.URI[idx+3:]
}

// LoadActionProvisioners reads all *.provisioners.yaml files in dir (sorted
// lexicographically by name) and returns entries that have a uri field set.
func LoadActionProvisioners(dir string) ([]ActionProvisioner, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.action-provisioners.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	var all []ActionProvisioner
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		var entries []ActionProvisioner
		if err := yaml.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		for _, e := range entries {
			if e.URI != "" {
				all = append(all, e)
			}
		}
	}
	return all, nil
}

// Match returns the first provisioner matching (resourceType, class, id) using
// the spec's three-level precedence rule:
//  1. resourceType + class + id  (most specific)
//  2. resourceType + class
//  3. resourceType               (least specific)
func Match(provisioners []ActionProvisioner, resourceType, class, id string) (*ActionProvisioner, error) {
	if class != "" && id != "" {
		for i := range provisioners {
			p := &provisioners[i]
			if p.ResourceType == resourceType && p.Class == class && p.ID == id {
				return p, nil
			}
		}
	}
	if class != "" {
		for i := range provisioners {
			p := &provisioners[i]
			if p.ResourceType == resourceType && p.Class == class {
				return p, nil
			}
		}
	}
	for i := range provisioners {
		p := &provisioners[i]
		if p.ResourceType == resourceType {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no provisioner found for resourceType=%q class=%q id=%q", resourceType, class, id)
}
