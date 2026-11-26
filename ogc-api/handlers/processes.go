package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"ogc-api/models"
	"ogc-api/store"
	"slices"
	"sort"
	"strings"
	"time"
)

// ProcessHandlers groups process-related route handlers.
type ProcessHandlers struct {
	Store *store.Store
}

// List handles GET /processes (/req/core/process-list). Supports limit
// (1..10000, default 10) with offset-based next/prev links.
func (h *ProcessHandlers) List(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryLimit(r, 10)
	if !ok {
		writeProblem(w, http.StatusBadRequest, ExcInvalidQueryParameter, "limit must be an integer between 1 and 10000")
		return
	}
	offset := max(queryInt(r, "offset", 0), 0)

	all := h.Store.ListProcesses()
	page := []models.ProcessSummary{}
	if offset < len(all) {
		page = all[offset:min(offset+limit, len(all))]
	}
	writeJSON(w, http.StatusOK, models.ProcessList{
		Processes: page,
		Links:     pageLinks(BaseURL+"/processes", nil, limit, offset, len(all)),
	})
}

// Describe handles GET /processes/{processId} (/req/ogc-process-description).
func (h *ProcessHandlers) Describe(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/processes/"), "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Missing process ID")
		return
	}
	if strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}

	p, ok := h.Store.GetProcess(id)
	if !ok {
		writeProblem(w, http.StatusNotFound, ExcNoSuchProcess, "Process not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Execute handles POST /processes/{processId}/execution (/req/core/process-execute-*).
func (h *ProcessHandlers) Execute(w http.ResponseWriter, r *http.Request) {
	// Extract processId: path is /processes/{id}/execution
	trimmed := strings.TrimPrefix(r.URL.Path, "/processes/")
	id := strings.TrimSuffix(trimmed, "/execution")

	desc, ok := processDescriptions[id]
	if !ok {
		writeProblem(w, http.StatusNotFound, ExcNoSuchProcess, "Process not found: "+id)
		return
	}

	var req models.ExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	if req.Inputs == nil {
		req.Inputs = make(map[string]any)
	}
	normalizeInputs(req.Inputs)
	apiKey := r.Header.Get("X-Api-Key")

	// Part 3: an input may be another execution request. Nested processes are
	// run first and their outputs substituted, so validation below sees ordinary
	// values. The top-level "process" key is ignored, as the standard requires:
	// the request URI already says which process this is.
	if hasNestedProcess(req.Inputs) {
		Logger.Info("resolving nested processes", "process", id)
		if err := resolveNestedInputs(req.Inputs, apiKey, 1); err != nil {
			status := errorStatus(err)
			writeProblem(w, status, failureType(status), err.Error())
			return
		}
	}

	if apiKey != "" {
		req.Inputs["_api_key"] = apiKey
	}

	if errs := validateInputs(desc, req.Inputs); len(errs) > 0 {
		Logger.Warn("invalid inputs", "process", id, "errors", errs)
		writeError(w, http.StatusBadRequest, strings.Join(errs, "; "))
		return
	}

	outputIDs, err := requestedOutputs(desc, req.Outputs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	prefer := r.Header.Get("Prefer")
	async, ok := resolveExecutionMode(prefer, desc)
	if !ok {
		writeError(w, http.StatusNotImplemented, "process advertises neither sync-execute nor async-execute")
		return
	}
	if applied := appliedPreference(prefer, async); applied != "" {
		w.Header().Set(headerPreferenceApplied, applied)
	}

	Logger.Info("executing process", "process", id, "async", async)

	if async {
		h.executeAsync(w, id, req, outputIDs)
	} else {
		h.executeSync(w, id, req, outputIDs)
	}
}

// requestedOutputs returns the sorted output IDs selected by the execute
// request's "outputs" member. Absent means all declared outputs.
func requestedOutputs(desc models.ProcessDescription, sel map[string]models.Output) ([]string, error) {
	if sel == nil {
		ids := make([]string, 0, len(desc.Outputs))
		for k := range desc.Outputs {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		return ids, nil
	}
	ids := make([]string, 0, len(sel))
	for k := range sel {
		if _, ok := desc.Outputs[k]; !ok {
			return nil, fmt.Errorf("unknown output: %q", k)
		}
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids, nil
}

// executeSync runs the process immediately and returns the result
// (table_core_execute_responses, 2.0):
//   - no outputs requested ("outputs": {}) → 204 No Content
//   - one output → 200 with the raw output value
//   - several outputs → 200 JSON results document with the ogc-results profile link
func (h *ProcessHandlers) executeSync(w http.ResponseWriter, processID string, req models.ExecutionRequest, outputIDs []string) {
	result, err := runProcess(processID, req.Inputs)
	if err != nil {
		Logger.Warn("process failed", "process", processID, "error", err.Error())
		status := errorStatus(err)
		writeProblem(w, status, failureType(status), err.Error())
		return
	}
	selected := selectOutputs(result, outputIDs)
	switch len(outputIDs) {
	case 0:
		w.WriteHeader(http.StatusNoContent)
	case 1:
		writeJSON(w, http.StatusOK, selected[outputIDs[0]])
	default:
		writeResultsDocument(w, selected)
	}
}

// executeAsync creates a job, runs in background, returns 201 with StatusInfo.
func (h *ProcessHandlers) executeAsync(w http.ResponseWriter, processID string, req models.ExecutionRequest, outputIDs []string) {
	jobID := h.Store.CreateJob(processURL(processID))

	go func() {
		h.Store.UpdateJob(jobID, models.JobStatusRunning, nil)
		if req.Subscriber != nil {
			if job, ok := h.Store.GetJob(jobID); ok {
				sendCallback(req.Subscriber.InProgressURI, job.Status)
			}
		}

		result, err := runProcess(processID, req.Inputs)

		// Don't overwrite status if the job was dismissed or removed while running.
		if current, ok := h.Store.GetJob(jobID); !ok || current.Status.Status == models.JobStatusDismissed {
			return
		}

		if err != nil {
			status := errorStatus(err)
			h.Store.FailJob(jobID, status, err.Error(), failureProblem(status, err.Error()))
			if req.Subscriber != nil {
				if job, ok := h.Store.GetJob(jobID); ok {
					sendCallback(req.Subscriber.FailedURI, job.Status)
				}
			}
			return
		}

		selected := selectOutputs(result, outputIDs)
		h.Store.UpdateJob(jobID, models.JobStatusSuccessful, selected)
		if req.Subscriber != nil {
			// Clause 14: on success the result response is sent to successUri.
			sendCallback(req.Subscriber.SuccessURI, wrapDocumentResult(selected))
		}
	}()

	job, _ := h.Store.GetJob(jobID)
	w.Header().Set("Location", BaseURL+"/jobs/"+jobID)
	writeJSON(w, http.StatusCreated, statusWithLinks(job.Status))
}

// normalizeInputs unwraps qualified input values ({"value": v, "mediaType": …})
// to the bare value, in place, so validation and processes see one shape.
// Arrays of values (maxOccurs > 1) are unwrapped element-wise.
func normalizeInputs(inputs map[string]any) {
	for k, v := range inputs {
		inputs[k] = unwrapQualified(v)
	}
}

func unwrapQualified(v any) any {
	switch t := v.(type) {
	case map[string]any:
		inner, ok := t["value"]
		if !ok {
			return v
		}
		for key := range t {
			switch key {
			case "value", "mediaType", "encoding", "schema":
			default:
				return v // an ordinary object that happens to have a "value" field
			}
		}
		return inner
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = unwrapQualified(e)
		}
		return out
	}
	return v
}

// callbackClient posts subscriber callbacks; the timeout keeps a hung
// subscriber from leaking a goroutine.
var callbackClient = &http.Client{Timeout: 30 * time.Second}

// sendCallback sends a best-effort HTTP POST to url with payload as JSON.
// A statusInfo payload gets its links added.
func sendCallback(url string, payload any) {
	if url == "" {
		return
	}
	if st, ok := payload.(models.StatusInfo); ok {
		payload = statusWithLinks(st)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		Logger.Warn("callback marshal failed", "url", url, "error", err.Error())
		return
	}
	resp, err := callbackClient.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		Logger.Warn("callback failed", "url", url, "error", err.Error())
		return
	}
	resp.Body.Close()
	Logger.Info("callback sent", "url", url, "status", resp.StatusCode)
}

// parsePrefer returns the respond-* preference from a Prefer header, which is
// a comma-separated list of preferences that may carry ";"-parameters.
// Preferences the server does not understand are ignored (RFC 7240 §2).
func parsePrefer(prefer string) (respond string) {
	for _, p := range strings.Split(prefer, ",") {
		token, _, _ := strings.Cut(p, ";")
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "respond-async" || token == "respond-sync" {
			respond = token
		}
	}
	return respond
}

// resolveExecutionMode determines whether to run async. A respond-* preference
// is honoured when the process supports that mode; otherwise the server uses
// the mode the process does support (/req/core/process-execute-auto-execution-mode).
// Without a preference, sync wins when available
// (/req/core/process-execute-default-execution-mode). ok is false only if the
// process advertises neither mode.
func resolveExecutionMode(prefer string, desc models.ProcessDescription) (async bool, ok bool) {
	canSync, canAsync := supportsMode(desc, "sync-execute"), supportsMode(desc, "async-execute")
	switch {
	case !canSync && !canAsync:
		return false, false
	case parsePrefer(prefer) == "respond-async" && canAsync:
		return true, true
	default:
		return !canSync, true
	}
}

// appliedPreference returns the Preference-Applied header value
// (/rec/core/process-execute-preference-applied), or "" if no respond-*
// preference was honoured.
func appliedPreference(prefer string, async bool) string {
	respond := parsePrefer(prefer)
	if (respond == "respond-async") == async && respond != "" {
		return respond
	}
	return ""
}

// supportsMode reports whether the process lists the given job control option.
func supportsMode(desc models.ProcessDescription, mode string) bool {
	return slices.Contains(desc.JobControlOptions, mode)
}

// runProcess looks up the process in the function registry and executes it.
func runProcess(processID string, inputs map[string]any) (map[string]any, error) {
	fn, ok := processFunctions[processID]
	if !ok {
		return nil, fmt.Errorf("no implementation for process: %s", processID)
	}
	return fn(inputs)
}
