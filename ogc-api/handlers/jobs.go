package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"ogc-api/models"
	"ogc-api/store"
	"slices"
	"strconv"
	"strings"
	"time"
)

// JobHandlers groups job-related route handlers.
type JobHandlers struct {
	Store *store.Store
}

var validStatuses = []string{
	models.JobStatusAccepted, models.JobStatusRunning, models.JobStatusSuccessful,
	models.JobStatusFailed, models.JobStatusDismissed,
}

// List handles GET /jobs (job-list conformance class). Supports the 2.0 query
// parameters processID, status, type, datetime, minDuration, maxDuration and limit.
func (h *JobHandlers) List(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryLimit(r, 10)
	if !ok {
		writeProblem(w, http.StatusBadRequest, ExcInvalidQueryParameter, "limit must be an integer between 1 and 10000")
		return
	}
	offset := max(queryInt(r, "offset", 0), 0)

	filter, err := parseJobFilter(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, ExcInvalidQueryParameter, err.Error())
		return
	}

	var matched []models.StatusInfo
	// type only knows "process"; asking for anything else matches no jobs.
	if types := queryList(r, "type"); len(types) == 0 || slices.Contains(types, "process") {
		matched = h.Store.FilterJobs(filter)
	}

	page := []models.StatusInfo{}
	if offset < len(matched) {
		for _, st := range matched[offset:min(offset+limit, len(matched))] {
			page = append(page, statusWithLinks(st))
		}
	}

	// Carry the filter parameters into the paging links.
	params := r.URL.Query()
	params.Del("limit")
	params.Del("offset")

	writeJSON(w, http.StatusOK, models.JobList{
		Jobs:  page,
		Links: pageLinks(BaseURL+"/jobs", params, limit, offset, len(matched)),
	})
}

// parseJobFilter reads the job-list filter parameters.
func parseJobFilter(r *http.Request) (store.JobFilter, error) {
	f := store.JobFilter{Statuses: queryList(r, "status")}
	// Accept process IDs or process URLs; the store matches either.
	for _, p := range queryList(r, "processID") {
		f.ProcessIDs = append(f.ProcessIDs, strings.TrimPrefix(p, BaseURL+"/processes/"))
	}
	for _, s := range f.Statuses {
		if !slices.Contains(validStatuses, s) {
			return f, fmt.Errorf("invalid status %q", s)
		}
	}
	if dt := r.URL.Query().Get("datetime"); dt != "" {
		from, to, err := parseDatetime(dt)
		if err != nil {
			return f, err
		}
		f.From, f.To = from, to
	}
	for _, p := range []struct {
		name string
		dst  **time.Duration
	}{{"minDuration", &f.MinDuration}, {"maxDuration", &f.MaxDuration}} {
		v := r.URL.Query().Get(p.name)
		if v == "" {
			continue
		}
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			return f, fmt.Errorf("%s must be a non-negative integer number of seconds", p.name)
		}
		d := time.Duration(secs) * time.Second
		*p.dst = &d
	}
	return f, nil
}

// parseDatetime parses an RFC 3339 instant or an interval "start/end" where
// either end may be ".." or empty (open).
func parseDatetime(v string) (from, to *time.Time, err error) {
	parse := func(s string) (*time.Time, error) {
		if s == "" || s == ".." {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("invalid datetime %q: must be RFC 3339", s)
		}
		return &t, nil
	}
	start, end, isInterval := strings.Cut(v, "/")
	if from, err = parse(start); err != nil {
		return nil, nil, err
	}
	if !isInterval {
		if from == nil {
			return nil, nil, fmt.Errorf("invalid datetime %q", v)
		}
		return from, from, nil
	}
	if to, err = parse(end); err != nil {
		return nil, nil, err
	}
	return from, to, nil
}

// pageLinks builds self/prev/next links for an offset-paginated list.
func pageLinks(base string, params url.Values, limit, offset, total int) []models.Link {
	href := func(off int) string {
		q := url.Values{}
		for k, v := range params {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(limit))
		if off > 0 {
			q.Set("offset", strconv.Itoa(off))
		}
		return base + "?" + q.Encode()
	}
	links := []models.Link{{Href: href(offset), Rel: "self", Type: "application/json"}}
	if offset > 0 {
		links = append(links, models.Link{Href: href(max(offset-limit, 0)), Rel: "prev", Type: "application/json"})
	}
	if offset+limit < total {
		links = append(links, models.Link{Href: href(offset + limit), Rel: "next", Type: "application/json"})
	}
	return links
}

// statusWithLinks returns st with its self/monitor links and, once
// successful, the results link.
func statusWithLinks(st models.StatusInfo) models.StatusInfo {
	self := BaseURL + "/jobs/" + st.ID
	st.Links = []models.Link{
		{Href: self, Rel: "self", Type: "application/json"},
		{Href: self, Rel: "monitor", Type: "application/json", Title: "Job status"},
	}
	if st.Status == models.JobStatusSuccessful {
		st.Links = append(st.Links, models.Link{
			Href:  self + "/results",
			Rel:   relOGCPrefix + "results",
			Type:  "application/json",
			Title: "Job results",
		})
	}
	return st
}

// Status handles GET /jobs/{jobId} (/req/core/job).
func (h *JobHandlers) Status(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "/jobs/")
	job, ok := h.Store.GetJob(id)
	if !ok {
		writeProblem(w, http.StatusNotFound, ExcNoSuchJob, "Job not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, statusWithLinks(job.Status))
}

// finishedResult returns the stored outputs of a successful job, or writes the
// appropriate problem response and returns ok=false.
func (h *JobHandlers) finishedResult(w http.ResponseWriter, id string) (*store.Job, map[string]any, bool) {
	job, ok := h.Store.GetJob(id)
	if !ok {
		writeProblem(w, http.StatusNotFound, ExcNoSuchJob, "Job not found: "+id)
		return nil, nil, false
	}
	switch job.Status.Status {
	case models.JobStatusSuccessful:
		result, _ := job.Result.(map[string]any)
		if len(result) == 0 {
			writeProblem(w, http.StatusNotFound, ExcResultNotAvailable, "Job finished without results: "+id)
			return nil, nil, false
		}
		return job, result, true
	case models.JobStatusFailed:
		// /req/core/job-results-failed: status and type describe the failure.
		status := job.ErrStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		detail := "Job failed: " + id
		if job.Status.Message != "" {
			detail += ": " + job.Status.Message
		}
		writeProblem(w, status, failureType(status), detail)
	case models.JobStatusDismissed:
		writeProblem(w, http.StatusNotFound, ExcResultNotAvailable, "Job was dismissed: "+id)
	default:
		writeProblem(w, http.StatusNotFound, ExcResultNotReady, "Results not ready, current status: "+job.Status.Status)
	}
	return nil, nil, false
}

// declaresOutput reports whether the job's process declares outputID.
func declaresOutput(job *store.Job, outputID string) bool {
	id := strings.TrimPrefix(job.Status.ProcessID, BaseURL+"/processes/")
	_, ok := processDescriptions[id].Outputs[outputID]
	return ok
}

// Results handles GET /jobs/{jobId}/results (/req/core/job-results). The
// optional outputs parameter selects a subset of the outputs.
func (h *JobHandlers) Results(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "/jobs/")
	job, result, ok := h.finishedResult(w, id)
	if !ok {
		return
	}
	var ids []string
	if _, present := r.URL.Query()["outputs"]; present {
		ids = queryList(r, "outputs")
		for _, o := range ids {
			if _, ok := result[o]; ok {
				continue
			}
			if declaresOutput(job, o) {
				// Defined by the process but not requested at execution time.
				writeProblem(w, http.StatusNotFound, ExcResultNotAvailable, fmt.Sprintf("output %q was not requested for this job", o))
			} else {
				writeProblem(w, http.StatusBadRequest, ExcInvalidQueryParameter, fmt.Sprintf("unknown output %q", o))
			}
			return
		}
	}
	writeResultsDocument(w, selectOutputs(result, ids))
}

// Result handles GET /jobs/{jobId}/results/{outputId} (/req/core/job-result-op):
// the raw value of one output plus the OGC-Output-Values-Count header.
func (h *JobHandlers) Result(w http.ResponseWriter, r *http.Request, id, outputID string) {
	job, result, ok := h.finishedResult(w, id)
	if !ok {
		return
	}
	v, ok := result[outputID]
	if !ok {
		if declaresOutput(job, outputID) {
			// /req/core/process-execute-success-async-outputs: defined but not requested → 404.
			writeProblem(w, http.StatusNotFound, ExcResultNotAvailable, fmt.Sprintf("output %q was not requested for this job", outputID))
			return
		}
		writeProblem(w, http.StatusBadRequest, ExcNoSuchOutput, "No such output: "+outputID)
		return
	}
	// Every output of the registered processes has maxOccurs 1.
	w.Header().Set(headerOutputValuesCount, "1")
	writeJSON(w, http.StatusOK, v)
}

// Dismiss handles DELETE /jobs/{jobId} (dismiss conformance class). Running
// or queued jobs are marked dismissed; jobs that already finished or were
// dismissed before are removed together with their results, as 2.0 requires
// (/req/dismiss/status). Every case answers 200 with a dismissed statusInfo.
func (h *JobHandlers) Dismiss(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "/jobs/")
	job, ok := h.Store.GetJob(id)
	if !ok {
		writeProblem(w, http.StatusNotFound, ExcNoSuchJob, "Job not found: "+id)
		return
	}
	switch job.Status.Status {
	case models.JobStatusSuccessful, models.JobStatusFailed, models.JobStatusDismissed:
		status := job.Status
		h.Store.RemoveJob(id)
		now := time.Now().UTC()
		status.Status = models.JobStatusDismissed
		status.Updated = &now
		status.Links = []models.Link{{Href: BaseURL + "/jobs", Rel: "up", Type: "application/json", Title: "Job list"}}
		writeJSON(w, http.StatusOK, status)
		return
	}
	h.Store.DismissJob(id)
	job, _ = h.Store.GetJob(id)
	writeJSON(w, http.StatusOK, statusWithLinks(job.Status))
}

// Route dispatches /jobs/* requests to the correct handler.
func (h *JobHandlers) Route(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/jobs"), "/")
	parts := strings.Split(path, "/")

	switch {
	case path == "":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.List(w, r)

	case len(parts) == 2 && parts[1] == "results":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.Results(w, r)

	// /jobs/{id}/results/{outputId} and /jobs/{id}/results/{outputId}/0 (the
	// first and only value, since every output has maxOccurs 1).
	case (len(parts) == 3 || (len(parts) == 4 && parts[3] == "0")) && parts[1] == "results":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.Result(w, r, parts[0], parts[2])

	case len(parts) == 1:
		switch r.Method {
		case http.MethodGet:
			h.Status(w, r)
		case http.MethodDelete:
			h.Dismiss(w, r)
		default:
			writeMethodNotAllowed(w, http.MethodGet, http.MethodDelete)
		}

	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}
