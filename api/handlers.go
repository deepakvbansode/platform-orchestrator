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
	"go.yaml.in/yaml/v3"
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
