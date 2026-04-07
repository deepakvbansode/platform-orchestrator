package action

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"
)

// TemplateContext is the data passed to all Go template blocks in a provisioner.
type TemplateContext struct {
	// Params holds spec.properties from the ActionSpec.
	Params map[string]interface{}
	// Operation is spec.operation (e.g., "create", "delete").
	Operation string
	// Guid is a unique identifier generated for this action execution.
	Guid string
	// Uid is spec.metadata.name.
	Uid string
	// SourceWorkload is the same as Uid (mirrors score-k8s convention).
	SourceWorkload string
	// Namespace is the target K8s namespace from the runner config.
	Namespace string
	// Context is the injected runtime context (user, team, etc.).
	Context ActionContext
	// State is populated after evaluating the provisioner's state: block.
	State map[string]string
}

// TemplateExecutor evaluates template:// provisioners.
type TemplateExecutor struct{}

// Execute renders the provisioner's init, state, and manifests blocks, then
// writes the resulting YAML to manifests.yaml in workDir. Returns the path to
// the written file.
func (e *TemplateExecutor) Execute(prov *ActionProvisioner, tctx TemplateContext, workDir string) (string, error) {
	funcMap := buildFuncMap()

	// Step 1: evaluate init: block for validation (output is discarded).
	if strings.TrimSpace(prov.Init) != "" {
		t, err := template.New("init").Funcs(funcMap).Parse(prov.Init)
		if err != nil {
			return "", fmt.Errorf("parse init block: %w", err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, tctx); err != nil {
			return "", fmt.Errorf("init validation failed: %w", err)
		}
	}

	// Step 2: evaluate state: block to populate tctx.State.
	if strings.TrimSpace(prov.State) != "" {
		t, err := template.New("state").Funcs(funcMap).Parse(prov.State)
		if err != nil {
			return "", fmt.Errorf("parse state block: %w", err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, tctx); err != nil {
			return "", fmt.Errorf("evaluate state block: %w", err)
		}
		stateMap := make(map[string]string)
		if err := yaml.Unmarshal(buf.Bytes(), &stateMap); err != nil {
			return "", fmt.Errorf("parse state block output as YAML: %w", err)
		}
		tctx.State = stateMap
	}

	// Step 3: evaluate manifests: block to generate K8s resources.
	if strings.TrimSpace(prov.Manifests) == "" {
		return "", fmt.Errorf("provisioner %q has no manifests block", prov.URI)
	}
	t, err := template.New("manifests").Funcs(funcMap).Parse(prov.Manifests)
	if err != nil {
		return "", fmt.Errorf("parse manifests block: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, tctx); err != nil {
		return "", fmt.Errorf("evaluate manifests block: %w", err)
	}

	// Write rendered manifests to workDir/manifests.yaml.
	manifestsPath := filepath.Join(workDir, "manifests.yaml")
	if err := os.WriteFile(manifestsPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write manifests.yaml: %w", err)
	}
	return manifestsPath, nil
}

// buildFuncMap returns a template.FuncMap with functions used by action provisioners.
// These mirror the subset of Sprig functions referenced in provisioner YAML files.
func buildFuncMap() template.FuncMap {
	return template.FuncMap{
		// default returns def when val is nil or the empty string.
		"default": func(def interface{}, val interface{}) interface{} {
			if val == nil || val == "" {
				return def
			}
			return val
		},
		// has reports whether item is in list.
		"has": func(item interface{}, list []interface{}) bool {
			for _, v := range list {
				if fmt.Sprintf("%v", v) == fmt.Sprintf("%v", item) {
					return true
				}
			}
			return false
		},
		// fail causes template execution to error with msg.
		"fail": func(msg string) (string, error) {
			return "", fmt.Errorf("%s", msg)
		},
		// substr returns str[start:end], clamping end to len(str).
		"substr": func(start, end int, str string) string {
			if end > len(str) {
				end = len(str)
			}
			return str[start:end]
		},
		// lower returns s in lowercase.
		"lower": strings.ToLower,
		// upper returns s in uppercase.
		"upper": strings.ToUpper,
		// list constructs a slice from its arguments.
		"list": func(items ...interface{}) []interface{} {
			return items
		},
		// dict constructs a map from alternating key, value arguments.
		"dict": func(pairs ...interface{}) map[string]interface{} {
			m := make(map[string]interface{})
			for i := 0; i+1 < len(pairs); i += 2 {
				key := fmt.Sprintf("%v", pairs[i])
				m[key] = pairs[i+1]
			}
			return m
		},
		// trimSpace trims whitespace from both ends.
		"trimSpace": strings.TrimSpace,
	}
}
