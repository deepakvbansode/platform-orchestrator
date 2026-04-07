package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/deepakvbansode/platform-orchestrator/internal/action"
	oerr "github.com/deepakvbansode/platform-orchestrator/internal/errors"
	"github.com/deepakvbansode/platform-orchestrator/internal/pipeline"
	"github.com/deepakvbansode/platform-orchestrator/internal/state"
	"go.yaml.in/yaml/v3"
)

// handleDeploy handles POST /api/v1/deploy.
// Accepts multipart/form-data or application/json (score field as base64).
func handleDeploy(runner *pipeline.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req pipeline.DeployRequest

		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(strings.ToLower(ct), "multipart/form-data"):
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid multipart form", err.Error(), "validate", 400))
				return
			}
			req.Org = r.FormValue("org")
			req.Env = r.FormValue("env")
			req.Workload = r.FormValue("workload")
			req.ImagePath = r.FormValue("image")
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
			Image    string `json:"image"` // optional docker image override
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid JSON body", err.Error(), "validate", 400))
				return
			}
			req.Org = body.Org
			req.Env = body.Env
			req.Workload = body.Workload
			req.ImagePath = body.Image
			decoded, err := base64.StdEncoding.DecodeString(body.Score)
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "score field must be base64-encoded", err.Error(), "validate", 400))
				return
			}
			req.ScoreYAML = decoded
		}

		result, oe := runner.Run(r.Context(), req)
		if oe != nil {
			writeError(w, oe)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

var identityRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validateIdentity(org, env, workload string) *oerr.OrchestratorError {
	for _, v := range []string{org, env, workload} {
		if !identityRe.MatchString(v) {
			return oerr.New(oerr.CodeInvalidInput, "org, env, and workload must match [a-zA-Z0-9_-]+", "", "validate", 400)
		}
	}
	return nil
}

// handleStatus handles GET /api/v1/workloads/{org}/{env}/{workload}/status.
func handleStatus(backend state.StateBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := chi.URLParam(r, "org")
		env := chi.URLParam(r, "env")
		workload := chi.URLParam(r, "workload")

		if oe := validateIdentity(org, env, workload); oe != nil {
			writeError(w, oe)
			return
		}

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

		if oe := validateIdentity(org, env, workload); oe != nil {
			writeError(w, oe)
			return
		}

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
		if _, err := w.Write(data); err != nil {
			log.Printf("handleManifest: write: %v", err)
		}
	}
}

func handleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleActionSubmit handles POST /api/v1/actions.
// Accepts multipart/form-data (action file) or application/json (action field as base64).
func handleActionSubmit(runner *action.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var actionYAML []byte

		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(strings.ToLower(ct), "multipart/form-data"):
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid multipart form", err.Error(), "validate", 400))
				return
			}
			f, _, err := r.FormFile("action")
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "action file missing from form", err.Error(), "validate", 400))
				return
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "failed to read action file", err.Error(), "validate", 400))
				return
			}
			actionYAML = data
		default:
			// JSON body with base64-encoded action field.
			var body struct {
				Action string `json:"action"` // base64-encoded platform-action.yaml
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid JSON body", err.Error(), "validate", 400))
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(body.Action)
			if err != nil {
				writeError(w, oerr.New(oerr.CodeInvalidInput, "action field must be base64-encoded", err.Error(), "validate", 400))
				return
			}
			actionYAML = decoded
		}

		var spec action.ActionSpec
		if err := yaml.Unmarshal(actionYAML, &spec); err != nil {
			writeError(w, oerr.New(oerr.CodeInvalidInput, "invalid action YAML", err.Error(), "validate", 400))
			return
		}

		// Inject runtime context from request headers (in production these would
		// come from the authenticated Backstage session via a trusted header or JWT).
		actCtx := action.ActionContext{
			User:        headerOrDefault(r, "X-User", "unknown"),
			Team:        headerOrDefault(r, "X-Team", ""),
			Role:        headerOrDefault(r, "X-Role", ""),
			Email:       headerOrDefault(r, "X-Email", ""),
			RequestedAt: time.Now().UTC().Format(time.RFC3339),
			RequestID:   fmt.Sprintf("act-%d", time.Now().UnixNano()),
		}

		req := action.ActionRequest{Spec: spec, Context: actCtx}
		result, runErr := runner.Run(r.Context(), req)
		if runErr != nil {
			writeError(w, oerr.New("ACTION_FAILED", runErr.Error(), "", "action", 500))
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

// handleActionStatus handles GET /api/v1/actions/{name}/status.
func handleActionStatus(stateBackend action.ActionStateBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" {
			writeError(w, oerr.New(oerr.CodeInvalidInput, "action name is required", "", "validate", 400))
			return
		}

		s, err := stateBackend.GetState(r.Context(), name)
		if err != nil {
			writeError(w, oerr.New(oerr.CodeStatePullFailed, "failed to read action state", err.Error(), "state", 500))
			return
		}
		if s == nil {
			writeError(w, oerr.New(oerr.CodeWorkloadNotFound, fmt.Sprintf("action %q not found", name), "", "", 404))
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

func headerOrDefault(r *http.Request, header, def string) string {
	if v := r.Header.Get(header); v != "" {
		return v
	}
	return def
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
			if uid == name || (len(uid) > len(name) && uid[:len(name)] == name && (uid[len(name)] == '.' || uid[len(name)] == '#')) {
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
