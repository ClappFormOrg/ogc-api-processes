package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ogc-api/models"
	"ogc-api/store"
	"os"
	"testing"
	"time"
)

// testAPIKey returns the API key for use in tests.
// Set the API_KEY environment variable (e.g. as a CI/CD pipeline secret) to
// use a real key; falls back to a placeholder for local runs where no upstream
// call is made (e.g. the echo process).
func testAPIKey() string {
	if key := os.Getenv("API_KEY"); key != "" {
		return key
	}
	return "test-key"
}

func init() {
	BaseURL = "http://test"
	// Register a minimal echo process as a test fixture.
	processRegistry = append(processRegistry, ProcessDefinition{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:                "echo",
				Version:           "1.0.0",
				JobControlOptions: []string{"sync-execute", "async-execute"},
			},
			Inputs: map[string]models.InputDescription{
				"message": {
					Title:     "Message",
					Schema:    map[string]string{"type": "string"},
					MinOccurs: 1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"result": {
					Title:  "Result",
					Schema: map[string]string{"type": "string"},
				},
			},
		},
		Fn: func(inputs map[string]any) (map[string]any, error) {
			msg, _ := inputs["message"].(string)
			return map[string]any{"result": msg}, nil
		},
	})
	// A two-output fixture for results-document responses.
	processRegistry = append(processRegistry, ProcessDefinition{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:                "echo2",
				Version:           "1.0.0",
				JobControlOptions: []string{"sync-execute", "async-execute"},
			},
			Inputs: map[string]models.InputDescription{
				"message": {Schema: map[string]string{"type": "string"}, MinOccurs: 1},
			},
			Outputs: map[string]models.OutputDescription{
				"result": {Schema: map[string]string{"type": "string"}},
				"length": {Schema: map[string]string{"type": "integer"}},
			},
		},
		Fn: func(inputs map[string]any) (map[string]any, error) {
			msg, _ := inputs["message"].(string)
			return map[string]any{"result": msg, "length": len(msg)}, nil
		},
	})
}

func executeRequest(ph *ProcessHandlers, processID, body, prefer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/processes/"+processID+"/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)
	return w
}

// ---------- Core ----------

func TestLandingPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	LandingPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var lp models.LandingPage
	if err := json.Unmarshal(w.Body.Bytes(), &lp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if lp.Title == "" {
		t.Error("expected non-empty title")
	}
	if len(lp.Links) < 3 {
		t.Errorf("expected at least 3 links, got %d", len(lp.Links))
	}
}

func TestLandingPage_NotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()
	LandingPage(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestOpenAPI(t *testing.T) {
	newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	w := httptest.NewRecorder()
	OpenAPI(w, req)

	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != openAPIContentType {
		t.Fatalf("expected 200 %s, got %d %s", openAPIContentType, w.Code, w.Header().Get("Content-Type"))
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// The Geonovum ogc-checker only accepts OpenAPI 3.0.x.
	if doc.OpenAPI != "3.0.3" {
		t.Errorf("expected openapi 3.0.3, got %q", doc.OpenAPI)
	}
	for path, method := range map[string]string{
		"/": "get", "/conformance": "get", "/processes": "get", "/processes/{processId}": "get",
		"/processes/{processId}/execution": "post", "/jobs": "get", "/jobs/{jobId}": "delete",
		"/jobs/{jobId}/results": "get", "/jobs/{jobId}/results/{outputId}": "get",
		"/jobs/{jobId}/results/{outputId}/0": "get",
	} {
		if doc.Paths[path][method] == nil {
			t.Errorf("missing %s %s", method, path)
		}
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(ogcSchemasBase+"processes-core/statusInfo.yaml")) {
		t.Error("expected references to the OGC 2.0 schemas")
	}
}

func TestConformance(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/conformance", nil)
	w := httptest.NewRecorder()
	Conformance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var c models.Conformance
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(c.ConformsTo) == 0 {
		t.Error("expected at least one conformance class")
	}

	// Check core is declared
	found := false
	for _, cc := range c.ConformsTo {
		if cc == "https://www.opengis.net/spec/ogcapi-processes-1/2.0/conf/core" {
			found = true
		}
	}
	if !found {
		t.Error("expected core conformance class")
	}
}

// ---------- Processes ----------

func newProcessHandlers() *ProcessHandlers {
	return NewProcessHandlers(store.New())
}

func TestProcessList(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes", nil)
	w := httptest.NewRecorder()
	ph.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var pl models.ProcessList
	json.Unmarshal(w.Body.Bytes(), &pl)
	if len(pl.Processes) == 0 {
		t.Error("expected at least one process")
	}
}

func TestProcessDescribe(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes/echo", nil)
	w := httptest.NewRecorder()
	ph.Describe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var pd models.ProcessDescription
	json.Unmarshal(w.Body.Bytes(), &pd)
	if pd.ID != "echo" {
		t.Errorf("expected ID 'echo', got %q", pd.ID)
	}
	if len(pd.Inputs) == 0 {
		t.Error("expected inputs to be defined")
	}
}

func TestProcessDescribe_NotFound(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes/nonexistent", nil)
	w := httptest.NewRecorder()
	ph.Describe(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestProcessExecute_Sync(t *testing.T) {
	ph := newProcessHandlers()
	// A single requested output is returned as the raw value (/req/core/process-execute-sync-one).
	body := `{"inputs":{"message":"hello world"}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "respond-sync")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var rawVal string
	if err := json.Unmarshal(w.Body.Bytes(), &rawVal); err != nil {
		t.Fatalf("expected raw string value, got: %s", w.Body.String())
	}
	if rawVal != "hello world" {
		t.Errorf("expected 'hello world', got %q", rawVal)
	}
}

func TestProcessExecute_SyncDocument(t *testing.T) {
	ph := newProcessHandlers()
	// Several outputs → JSON results document (/req/core/process-execute-sync-many-json).
	w := executeRequest(ph, "echo2", `{"inputs":{"message":"test"}}`, "respond-sync")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if link := w.Header().Get("Link"); link != linkProfileResults {
		t.Errorf("expected ogc-results profile Link header, got %q", link)
	}

	var result map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("expected results document, got %s", w.Body.String())
	}
	if result["result"]["value"] != "test" || result["length"]["value"] != 4.0 {
		t.Errorf("unexpected results document: %s", w.Body.String())
	}
}

func TestProcessExecute_SyncSelectedOutput(t *testing.T) {
	ph := newProcessHandlers()
	// Selecting one of several outputs returns that output raw.
	w := executeRequest(ph, "echo2", `{"inputs":{"message":"test"},"outputs":{"length":{}}}`, "respond-sync")

	if w.Code != http.StatusOK || w.Body.String() != "4\n" {
		t.Fatalf("expected 200 with raw value 4, got %d %q", w.Code, w.Body.String())
	}
}

func TestProcessExecute_SyncNoOutputs(t *testing.T) {
	ph := newProcessHandlers()
	w := executeRequest(ph, "echo2", `{"inputs":{"message":"test"},"outputs":{}}`, "respond-sync")

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for empty outputs, got %d", w.Code)
	}
}

func TestProcessExecute_UnknownOutput(t *testing.T) {
	ph := newProcessHandlers()
	w := executeRequest(ph, "echo", `{"inputs":{"message":"test"},"outputs":{"nope":{}}}`, "respond-sync")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown output, got %d", w.Code)
	}
}

func TestProcessExecute_QualifiedInput(t *testing.T) {
	ph := newProcessHandlers()
	w := executeRequest(ph, "echo", `{"inputs":{"message":{"value":"qualified","mediaType":"text/plain"}}}`, "respond-sync")

	if w.Code != http.StatusOK || w.Body.String() != "\"qualified\"\n" {
		t.Fatalf("expected qualified input to be unwrapped, got %d %q", w.Code, w.Body.String())
	}
}

func TestProcessExecute_SyncProcessError(t *testing.T) {
	ph := newProcessHandlers()
	// Invalid plot geometry is an input error → 400, not 200 with an error output.
	body := `{"inputs":{"plot":{"plan_dataid":"x","geometry":{"type":"Point","coordinates":[5,52]}}}}`
	w := executeRequest(ph, "plot-bag-matching", body, "respond-sync")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid plot geometry, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProcessExecute_PlotBagMatching(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{
		"plot":{"plan_dataid":"test-plan-1","geometry":{"type":"Polygon","coordinates":[[[5.000,52.000],[5.002,52.000],[5.002,52.002],[5.000,52.002],[5.000,52.000]]]}},
		"bag_buildings":[{"type":"Feature","properties":{"identificatie":"NL.IMBAG.PAND.0001","bouwjaar":2019},
			"geometry":{"type":"Polygon","coordinates":[[[5.0005,52.0005],[5.0008,52.0005],[5.0008,52.0008],[5.0005,52.0008],[5.0005,52.0005]]]}}]
	}}`
	w := executeRequest(ph, "plot-bag-matching", body, "respond-sync")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Matches []struct {
			BagID     string `json:"bag_id"`
			MatchType string `json:"match_type"`
		} `json:"matches"`
		Summary struct {
			Flagged bool `json:"flagged"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("expected raw match_result, got %s", w.Body.String())
	}
	if len(res.Matches) != 1 || res.Matches[0].MatchType != "full" || res.Summary.Flagged {
		t.Errorf("unexpected match result: %s", w.Body.String())
	}
}

func TestProcessList_Limit(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes?limit=1", nil)
	w := httptest.NewRecorder()
	ph.List(w, req)

	var pl models.ProcessList
	json.Unmarshal(w.Body.Bytes(), &pl)
	if len(pl.Processes) != 1 {
		t.Fatalf("expected 1 process with limit=1, got %d", len(pl.Processes))
	}
	hasNext := false
	for _, l := range pl.Links {
		hasNext = hasNext || l.Rel == "next"
	}
	if !hasNext {
		t.Error("expected a next link")
	}

	req = httptest.NewRequest(http.MethodGet, "/processes?limit=0", nil)
	w = httptest.NewRecorder()
	ph.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for limit=0, got %d", w.Code)
	}
}

func TestProcessDescribe_ProfileLink(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes/plot-bag-matching", nil)
	w := httptest.NewRecorder()
	ph.Describe(w, req)

	var pd models.ProcessDescription
	json.Unmarshal(w.Body.Bytes(), &pd)
	found := false
	for _, l := range pd.Links {
		found = found || (l.Rel == "profile" && l.Href == profileProcessDescription)
	}
	if !found {
		t.Errorf("expected ogc-process-description profile link, got %+v", pd.Links)
	}
}

func TestProcessExecute_Async(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"message":"async test"}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "respond-async")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if loc == "" {
		t.Error("expected Location header to be set")
	}

	var status models.StatusInfo
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.ID == "" {
		t.Error("expected id in response")
	}
	if status.ProcessingEntityType != models.ProcessingEntityType {
		t.Errorf("expected processingEntityType %q, got %q", models.ProcessingEntityType, status.ProcessingEntityType)
	}
	if w.Header().Get(headerPreferenceApplied) != "respond-async" {
		t.Errorf("expected Preference-Applied: respond-async, got %q", w.Header().Get(headerPreferenceApplied))
	}
	// statusInfo.processID is format: uri in 2.0.
	if status.ProcessID != "http://test/processes/echo" {
		t.Errorf("expected processID to be the process URL, got %q", status.ProcessID)
	}
}

func TestProcessExecute_NotFound(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/nonexistent/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestProcessExecute_BadBody(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---------- Jobs ----------

func newJobHandlers() (*JobHandlers, *store.Store) {
	s := store.New()
	return &JobHandlers{Store: s}, s
}

func TestJobList_Empty(t *testing.T) {
	jh, _ := newJobHandlers()
	req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	w := httptest.NewRecorder()
	jh.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var jl models.JobList
	json.Unmarshal(w.Body.Bytes(), &jl)
	if len(jl.Jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jl.Jobs))
	}
}

func TestJobStatus(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	jh.Status(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var si models.StatusInfo
	json.Unmarshal(w.Body.Bytes(), &si)
	if si.ID != id {
		t.Errorf("expected id %q, got %q", id, si.ID)
	}
}

func TestJobStatus_NotFound(t *testing.T) {
	jh, _ := newJobHandlers()
	req := httptest.NewRequest(http.MethodGet, "/jobs/fake-id", nil)
	w := httptest.NewRecorder()
	jh.Status(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobResults(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")
	s.UpdateJob(id, models.JobStatusRunning, nil)
	s.UpdateJob(id, models.JobStatusSuccessful, map[string]any{"result": "done"})

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/results", nil)
	w := httptest.NewRecorder()
	jh.Results(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["result"]["value"] != "done" {
		t.Errorf("expected result value 'done', got %v", result["result"])
	}
}

func TestJobResult_SingleOutput(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")
	s.UpdateJob(id, models.JobStatusSuccessful, map[string]any{"result": "done"})

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/results/result", nil)
	w := httptest.NewRecorder()
	jh.Route(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "\"done\"\n" {
		t.Fatalf("expected 200 with raw value, got %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get(headerOutputValuesCount) != "1" {
		t.Errorf("expected %s: 1", headerOutputValuesCount)
	}

	req = httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/results/nope", nil)
	w = httptest.NewRecorder()
	jh.Route(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown output, got %d", w.Code)
	}
}

func TestJobResults_Failed(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")
	s.FailJob(id, http.StatusBadGateway, "upstream down", nil)

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/results", nil)
	w := httptest.NewRecorder()
	jh.Results(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected the job's failure status 502, got %d", w.Code)
	}
}

func TestJobList_Filter(t *testing.T) {
	jh, s := newJobHandlers()
	s.CreateJob("echo")
	done := s.CreateJob("other")
	s.UpdateJob(done, models.JobStatusSuccessful, map[string]any{})

	for query, want := range map[string]int{
		"":                                  2,
		"?processID=other":                  1,
		"?status=accepted":                  1,
		"?status=accepted,successful":       2,
		"?type=process":                     2,
		"?type=openeo":                      0,
		"?datetime=2000-01-01T00:00:00Z/..": 2,
		"?datetime=../2000-01-01T00:00:00Z": 0,
	} {
		req := httptest.NewRequest(http.MethodGet, "/jobs"+query, nil)
		w := httptest.NewRecorder()
		jh.List(w, req)
		var jl models.JobList
		json.Unmarshal(w.Body.Bytes(), &jl)
		if len(jl.Jobs) != want {
			t.Errorf("%q: expected %d jobs, got %d", query, want, len(jl.Jobs))
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs?status=bogus", nil)
	w := httptest.NewRecorder()
	jh.List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d", w.Code)
	}
}

func TestJobResults_NotComplete(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"/results", nil)
	w := httptest.NewRecorder()
	jh.Results(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobDismiss(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")

	req := httptest.NewRequest(http.MethodDelete, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	jh.Dismiss(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	job, _ := s.GetJob(id)
	if job.Status.Status != models.JobStatusDismissed {
		t.Errorf("expected status 'dismissed', got %q", job.Status.Status)
	}
}

func TestJobDismiss_FinishedJobRemoved(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")
	s.UpdateJob(id, models.JobStatusSuccessful, map[string]any{"result": "done"})

	req := httptest.NewRequest(http.MethodDelete, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	jh.Dismiss(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if _, ok := s.GetJob(id); ok {
		t.Error("expected finished job to be removed on dismiss")
	}
}

func TestJobDismiss_AlreadyDismissed(t *testing.T) {
	jh, s := newJobHandlers()
	id := s.CreateJob("echo")
	s.DismissJob(id)

	req := httptest.NewRequest(http.MethodDelete, "/jobs/"+id, nil)
	w := httptest.NewRecorder()
	jh.Dismiss(w, req)

	// /req/dismiss/status: a dismissed job is removed and 200 is returned.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if _, ok := s.GetJob(id); ok {
		t.Error("expected dismissed job to be removed")
	}
}

func TestJobDismiss_NotFound(t *testing.T) {
	jh, _ := newJobHandlers()
	req := httptest.NewRequest(http.MethodDelete, "/jobs/fake-id", nil)
	w := httptest.NewRecorder()
	jh.Dismiss(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobRoute_MethodNotAllowed(t *testing.T) {
	jh, _ := newJobHandlers()
	req := httptest.NewRequest(http.MethodPost, "/jobs", nil)
	w := httptest.NewRecorder()
	jh.Route(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET" {
		t.Errorf("expected Allow: GET, got %q", allow)
	}

	req = httptest.NewRequest(http.MethodPut, "/jobs/some-id", nil)
	w = httptest.NewRecorder()
	jh.Route(w, req)
	if allow := w.Header().Get("Allow"); allow != "GET, DELETE" {
		t.Errorf("expected Allow: GET, DELETE, got %q", allow)
	}
}

func TestProcessExecute_MissingRequiredInput(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing required input, got %d", w.Code)
	}
}

func TestProcessExecute_WrongInputType(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"message":42}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong input type, got %d", w.Code)
	}
}

func TestProcessExecute_RespondSync(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"message":"sync"}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "respond-sync")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for respond-sync, got %d", w.Code)
	}
}

func TestProcessExecute_UnknownPreferIgnored(t *testing.T) {
	ph := newProcessHandlers()
	// RFC 7240: preferences the server does not understand are ignored.
	w := executeRequest(ph, "echo", `{"inputs":{"message":"hello"}}`, "respond-magic")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (sync default) for unknown preference, got %d", w.Code)
	}
	if applied := w.Header().Get(headerPreferenceApplied); applied != "" {
		t.Errorf("expected no Preference-Applied, got %q", applied)
	}
}

func TestProcessExecute_AsyncNotSupported(t *testing.T) {
	ph := newProcessHandlers()
	// Override echo's description to simulate a sync-only process.
	original := processDescriptions["echo"]
	syncOnly := original
	syncOnly.JobControlOptions = []string{"sync-execute"}
	processDescriptions["echo"] = syncOnly
	defer func() { processDescriptions["echo"] = original }()

	body := `{"inputs":{"message":"hello"}}`
	req := httptest.NewRequest(http.MethodPost, "/processes/echo/execution", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "respond-async")
	req.Header.Set("X-Api-Key", testAPIKey())
	w := httptest.NewRecorder()
	ph.Execute(w, req)

	// /req/core/process-execute-auto-execution-mode: fall back to sync.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (sync fallback) when async not supported, got %d", w.Code)
	}
	if applied := w.Header().Get(headerPreferenceApplied); applied != "" {
		t.Errorf("expected no Preference-Applied when the preference was not honoured, got %q", applied)
	}
}

func TestProcessExecute_SchemaValidation(t *testing.T) {
	ph := newProcessHandlers()
	square := `{"type":"Polygon","coordinates":[[[5.0,52.0],[5.002,52.0],[5.002,52.002],[5.0,52.002],[5.0,52.0]]]}`
	cases := map[string]string{
		"missing nested required": `{"inputs":{"plot":{"geometry":` + square + `}}}`,
		"enum violation":          `{"inputs":{"plot":{"plan_dataid":"x","geometry":{"type":"Point","coordinates":[5,52]}}}}`,
		"fractional integer":      `{"inputs":{"plot":{"plan_dataid":"x","planned_netto":1.5,"geometry":` + square + `}}}`,
		"below minimum":           `{"inputs":{"plot":{"plan_dataid":"x","geometry":` + square + `},"min_overlap_ratio":-0.1}}`,
		"by reference":            `{"inputs":{"plot":{"href":"https://example.org/plot.json"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := executeRequest(ph, "plot-bag-matching", body, "respond-sync")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProcessDescribe_OccurrencesAndValuePassing(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes/plot-bag-matching", nil)
	w := httptest.NewRecorder()
	ph.Describe(w, req)

	var raw struct {
		Inputs map[string]map[string]any `json:"inputs"`
	}
	json.Unmarshal(w.Body.Bytes(), &raw)
	opt := raw.Inputs["bag_buildings"]
	if opt["minOccurs"] != 0.0 {
		t.Errorf("optional input must state minOccurs 0, got %v", opt["minOccurs"])
	}
	if vp, _ := opt["valuePassing"].([]any); len(vp) != 1 || vp[0] != "byValue" {
		t.Errorf(`expected valuePassing ["byValue"], got %v`, opt["valuePassing"])
	}
}

func TestProcessDescribe_ExtraSegmentNotFound(t *testing.T) {
	ph := newProcessHandlers()
	req := httptest.NewRequest(http.MethodGet, "/processes/echo/extra", nil)
	w := httptest.NewRecorder()
	ph.Describe(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobResult_UnrequestedOutput(t *testing.T) {
	ph := newProcessHandlers()
	jh := &JobHandlers{Store: ph.Store}
	id := ph.Store.CreateJob(processURL("echo2"))
	ph.Store.UpdateJob(id, models.JobStatusSuccessful, map[string]any{"length": 4})

	for path, want := range map[string]int{
		"/jobs/" + id + "/results/result":          http.StatusNotFound, // defined, not requested
		"/jobs/" + id + "/results/length/0":        http.StatusOK,       // single value by index
		"/jobs/" + id + "/results?outputs=result":  http.StatusNotFound,
		"/jobs/" + id + "/results?outputs=unknown": http.StatusBadRequest,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		jh.Route(w, req)
		if w.Code != want {
			t.Errorf("%s: expected %d, got %d", path, want, w.Code)
		}
	}
}

func TestJobResults_FailedExceptionType(t *testing.T) {
	ph := newProcessHandlers()
	jh := &JobHandlers{Store: ph.Store}
	body := `{"inputs":{"plot":{"plan_dataid":"x","geometry":{"type":"Polygon","coordinates":[[[5,52],[5.1,52],[5,52]]]}}}}`
	w := executeRequest(ph, "plot-bag-matching", body, "respond-async")
	var st models.StatusInfo
	json.Unmarshal(w.Body.Bytes(), &st)

	var job *store.Job
	for i := 0; i < 200; i++ {
		if j, _ := ph.Store.GetJob(st.ID); j != nil && j.Status.Status == models.JobStatusFailed {
			job = j
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if job == nil {
		t.Fatal("job did not fail")
	}
	if job.Status.Exception == nil || job.Status.Exception.Type != ExcInvalidInput {
		t.Errorf("expected statusInfo.exception of type %s, got %+v", ExcInvalidInput, job.Status.Exception)
	}

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+st.ID+"/results", nil)
	rec := httptest.NewRecorder()
	jh.Results(rec, req)
	var prob models.ErrorResponse
	json.Unmarshal(rec.Body.Bytes(), &prob)
	if rec.Code != http.StatusBadRequest || prob.Type != ExcInvalidInput {
		t.Errorf("expected 400 %s, got %d %s", ExcInvalidInput, rec.Code, prob.Type)
	}
}
