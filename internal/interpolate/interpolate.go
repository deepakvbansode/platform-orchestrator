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
