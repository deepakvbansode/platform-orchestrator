package pipeline

import (
	"fmt"
	"regexp"

	oerr "github.com/score-spec/score-orchestrator/internal/errors"
	"go.yaml.in/yaml/v3"
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
