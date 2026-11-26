package handlers

import (
	"encoding/json"
	"net/http"
	"ogc-api/models"
	"strconv"
	"strings"
)

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// OGC exception types (OGC API - Processes 2.0, clause 7). The draft writes
// no-such-output with http://; the others use https://.
const (
	excPrefix                 = "https://www.opengis.net/def/exceptions/ogcapi-processes-1/1.0/"
	ExcNoSuchProcess          = excPrefix + "no-such-process"
	ExcNoSuchJob              = excPrefix + "no-such-job"
	ExcResultNotReady         = excPrefix + "result-not-ready"
	ExcResultNotAvailable     = excPrefix + "result-not-available"
	ExcInvalidQueryParameter  = excPrefix + "invalid-query-parameter-value"
	ExcNoSuchOutput           = "http://www.opengis.net/def/exceptions/ogcapi-processes-1/1.0/no-such-output"
	problemContentType        = "application/problem+json"
	linkProfileResults        = `<https://www.opengis.net/def/profile/OGC/0/ogc-results>; rel="profile"`
	profileProcessDescription = "https://www.opengis.net/def/profile/OGC/0/ogc-process-description"
	relOGCPrefix              = "https://www.opengis.net/def/rel/ogc/1.0/"
	headerOutputValuesCount   = "OGC-Output-Values-Count"
	headerPreferenceApplied   = "Preference-Applied"
)

// Problem types for process failures. OGC defines no types for these, so the
// server uses its own URNs; /req/core/job-results-failed asks for a type that
// corresponds to the reason for the failure.
const (
	ExcInvalidInput    = "urn:ogc-api:exception:invalid-input"
	ExcUpstreamFailure = "urn:ogc-api:exception:upstream-failure"
	ExcProcessFailed   = "urn:ogc-api:exception:process-failed"
)

// failureType returns the problem type for a process failure with the given HTTP status.
func failureType(status int) string {
	switch {
	case status == http.StatusBadGateway:
		return ExcUpstreamFailure
	case status >= 400 && status < 500:
		return ExcInvalidInput
	default:
		return ExcProcessFailed
	}
}

// failureProblem builds the problem details for a failed process execution.
func failureProblem(status int, detail string) *models.ErrorResponse {
	return &models.ErrorResponse{
		Type:   failureType(status),
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// writeMethodNotAllowed writes a 405 with the Allow header required by RFC 9110.
func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

// WriteMethodNotAllowed is the exported version of writeMethodNotAllowed.
func WriteMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	writeMethodNotAllowed(w, allowed...)
}

// processURL returns the absolute URL of a process, used as statusInfo.processID.
func processURL(id string) string { return BaseURL + "/processes/" + id }

// writeError writes an RFC 9457 Problem Details error response with type about:blank.
func writeError(w http.ResponseWriter, status int, detail string) {
	writeProblem(w, status, "about:blank", detail)
}

// writeProblem writes an RFC 9457 Problem Details response with the given type URI.
func writeProblem(w http.ResponseWriter, status int, typeURI, detail string) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Type:   typeURI,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

// WriteError is the exported version of writeError for use outside this package.
func WriteError(w http.ResponseWriter, status int, detail string) {
	writeError(w, status, detail)
}

// pathParam extracts a path segment by position from a cleaned path.
// For "/processes/{id}" with base "/processes/", pass the trimmed suffix.
func pathParam(r *http.Request, prefix string) string {
	p := strings.TrimPrefix(r.URL.Path, prefix)
	p = strings.TrimSuffix(p, "/")
	// Return the first segment
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

// wrapDocumentResult builds an OGC results document (results.yaml, 2.0).
// Each output value v becomes a qualifiedValue {"value": v}: objects must be
// wrapped, and wrapping everything keeps arrays unambiguous. Non-map results
// are returned as-is.
func wrapDocumentResult(result any) any {
	m, ok := result.(map[string]any)
	if !ok {
		return result
	}
	wrapped := make(map[string]any, len(m))
	for k, v := range m {
		wrapped[k] = map[string]any{"value": v}
	}
	return wrapped
}

// writeResultsDocument writes a JSON results document with the ogc-results profile link.
func writeResultsDocument(w http.ResponseWriter, outputs map[string]any) {
	w.Header().Set("Link", linkProfileResults)
	writeJSON(w, http.StatusOK, wrapDocumentResult(outputs))
}

// selectOutputs returns the subset of result whose keys are in ids.
// A nil ids slice selects everything.
func selectOutputs(result map[string]any, ids []string) map[string]any {
	if ids == nil {
		return result
	}
	out := make(map[string]any, len(ids))
	for _, id := range ids {
		if v, ok := result[id]; ok {
			out[id] = v
		}
	}
	return out
}

// queryLimit reads the limit parameter (1..10000, default def). ok is false
// when the value is present but invalid.
func queryLimit(r *http.Request, def int) (limit int, ok bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 10000 {
		return 0, false
	}
	return n, true
}

// queryList reads a query parameter that may be repeated and/or comma-separated.
func queryList(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.URL.Query()[key] {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// queryInt reads an integer query param with a default value.
func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
