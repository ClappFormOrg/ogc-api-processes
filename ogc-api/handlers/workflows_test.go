package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Fixtures shared by the workflow tests: a plot and one building fully inside
// it. No PDOK step is involved, so these run without network access.
const (
	wfGeometry  = `{"type":"Polygon","coordinates":[[[5.000,52.000],[5.002,52.000],[5.002,52.002],[5.000,52.002],[5.000,52.000]]]}`
	wfBuildings = `[{"type":"Feature","properties":{"identificatie":"NL.IMBAG.PAND.0001","bouwjaar":2019},` +
		`"geometry":{"type":"Polygon","coordinates":[[[5.0005,52.0005],[5.0008,52.0005],[5.0008,52.0008],[5.0005,52.0008],[5.0005,52.0005]]]}}]`
)

// problemDetail pulls the detail out of an application/problem+json body.
func problemDetail(t *testing.T, body []byte) string {
	t.Helper()
	var p struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("expected a problem document, got %s", body)
	}
	return p.Detail
}

// TestWorkflow_NestedChainMatchesSingleProcess is the claim the nested-processes
// class rests on here: chaining the step processes inside one execution request
// produces the same match_result as calling plot-bag-matching directly.
func TestWorkflow_NestedChainMatchesSingleProcess(t *testing.T) {
	ph := newProcessHandlers()

	// The whole recipe as one Part 3 request: classify <- overlap <- prepare.
	nested := `{"inputs":{
		"plan_dataid":"test-plan-1",
		"overlaps":{
			"process":"plot-bag-overlap",
			"inputs":{
				"geometry":{
					"process":"plot-geometry-prepare",
					"inputs":{"geometry":` + wfGeometry + `},
					"outputs":{"geometry":{}}
				},
				"bag_buildings":` + wfBuildings + `
			},
			"outputs":{"overlaps":{}}
		}
	},"outputs":{"match_result":{}}}`

	single := `{"inputs":{
		"plot":{"plan_dataid":"test-plan-1","geometry":` + wfGeometry + `},
		"bag_buildings":` + wfBuildings + `
	},"outputs":{"match_result":{}}}`

	wNested := executeRequest(ph, "bag-match-classify", nested, "respond-sync")
	if wNested.Code != http.StatusOK {
		t.Fatalf("nested chain: expected 200, got %d: %s", wNested.Code, wNested.Body.String())
	}
	wSingle := executeRequest(ph, "plot-bag-matching", single, "respond-sync")
	if wSingle.Code != http.StatusOK {
		t.Fatalf("single process: expected 200, got %d: %s", wSingle.Code, wSingle.Body.String())
	}

	var gotNested, gotSingle any
	if err := json.Unmarshal(wNested.Body.Bytes(), &gotNested); err != nil {
		t.Fatalf("nested body: %v", err)
	}
	if err := json.Unmarshal(wSingle.Body.Bytes(), &gotSingle); err != nil {
		t.Fatalf("single body: %v", err)
	}
	a, _ := json.Marshal(gotNested)
	b, _ := json.Marshal(gotSingle)
	if string(a) != string(b) {
		t.Fatalf("nested chain and plot-bag-matching disagree\nnested: %s\nsingle: %s", a, b)
	}
}

// TestWorkflow_NestedOutputCrossesAsJSON covers the conversion that in-process
// resolution would otherwise skip. plot-bbox returns a Go [4]float64, while
// bag-panden-fetch requires what encoding/json produces, a []any. Over HTTP the
// conversion is free; nested, it has to be done explicitly.
func TestWorkflow_NestedOutputCrossesAsJSON(t *testing.T) {
	ph := newProcessHandlers()
	// plot-bbox feeds plot-bag-overlap nothing useful, so assert on the bbox
	// shape by way of a process that insists on an array: bag-panden-fetch
	// validates bbox before any network call, so a bad shape fails fast.
	body := `{"inputs":{"bbox":{
		"process":"plot-bbox",
		"inputs":{"geometry":` + wfGeometry + `}
	}},"outputs":{"bag_query":{}}}`

	w := executeRequest(ph, "bag-panden-fetch", body, "respond-sync")
	// The fetch itself may fail without network, but it must not fail on the
	// shape of bbox - that would mean the Go array reached the validator.
	if detail := w.Body.String(); strings.Contains(detail, "must be an array") {
		t.Fatalf("nested bbox did not cross as JSON: %s", detail)
	}
}

func TestWorkflow_ProcessReferenceForms(t *testing.T) {
	ph := newProcessHandlers()

	cases := []struct {
		name   string
		ref    string
		status int
		detail string
	}{
		{"bare id", `"plot-geometry-prepare"`, http.StatusOK, ""},
		{"absolute uri on this server", `"http://test/processes/plot-geometry-prepare"`, http.StatusOK, ""},
		{"local path", `"/processes/plot-geometry-prepare"`, http.StatusOK, ""},
		{"remote server", `"https://elsewhere.example.com/processes/buffer"`, http.StatusBadRequest, "remote-core-processes"},
		{"unknown process", `"no-such-process"`, http.StatusBadRequest, "nested process not found"},
		{"empty reference", `""`, http.StatusBadRequest, "empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"inputs":{
				"plan_dataid":"p1",
				"overlaps":{
					"process":"plot-bag-overlap",
					"inputs":{
						"geometry":{"process":` + tc.ref + `,"inputs":{"geometry":` + wfGeometry + `},"outputs":{"geometry":{}}},
						"bag_buildings":` + wfBuildings + `
					},
					"outputs":{"overlaps":{}}
				}
			},"outputs":{"match_result":{}}}`

			w := executeRequest(ph, "bag-match-classify", body, "respond-sync")
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, w.Code, w.Body.String())
			}
			if tc.detail != "" && !strings.Contains(problemDetail(t, w.Body.Bytes()), tc.detail) {
				t.Errorf("expected the error to mention %q, got %s", tc.detail, w.Body.String())
			}
		})
	}
}

// TestWorkflow_OutputSelection covers the rule that a nested process stands
// where one value is expected, so it must resolve to exactly one output.
func TestWorkflow_OutputSelection(t *testing.T) {
	ph := newProcessHandlers()

	cases := []struct {
		name    string
		outputs string
		status  int
		detail  string
	}{
		{
			name:    "named output",
			outputs: `,"outputs":{"geometry":{}}`,
			status:  http.StatusOK,
		},
		{
			name:    "multi-output process without a selection",
			outputs: "",
			status:  http.StatusBadRequest,
			detail:  "must be named in its 'outputs'",
		},
		{
			name:    "two outputs requested",
			outputs: `,"outputs":{"geometry":{},"geometry_report":{}}`,
			status:  http.StatusBadRequest,
			detail:  "supplies one input value",
		},
		{
			name:    "output the process does not have",
			outputs: `,"outputs":{"nope":{}}`,
			status:  http.StatusBadRequest,
			detail:  "has no output",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"inputs":{
				"geometry":{"process":"plot-geometry-prepare","inputs":{"geometry":` + wfGeometry + `}` + tc.outputs + `}
			},"outputs":{"bbox":{}}}`

			w := executeRequest(ph, "plot-bbox", body, "respond-sync")
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, w.Code, w.Body.String())
			}
			if tc.detail != "" && !strings.Contains(problemDetail(t, w.Body.Bytes()), tc.detail) {
				t.Errorf("expected the error to mention %q, got %s", tc.detail, w.Body.String())
			}
		})
	}
}

// TestWorkflow_SingleOutputNeedsNoSelection: plot-bbox has one output, so a
// nested reference to it is unambiguous without an outputs block.
func TestWorkflow_SingleOutputNeedsNoSelection(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"bbox":{"process":"plot-bbox","inputs":{"geometry":` + wfGeometry + `}}},
		"outputs":{"bag_query":{}}}`
	w := executeRequest(ph, "bag-panden-fetch", body, "respond-sync")
	if detail := w.Body.String(); strings.Contains(detail, "must be named in its 'outputs'") {
		t.Fatalf("a single-output process should need no selection: %s", detail)
	}
}

// TestWorkflow_DepthLimit: a cycle shows up as a depth overflow rather than a
// hang, which is what the bound is there for.
func TestWorkflow_DepthLimit(t *testing.T) {
	ph := newProcessHandlers()

	// plot-bbox taking a prepared geometry taking a bbox... nested past the
	// limit. Built from the inside out.
	inner := wfGeometry
	for i := 0; i < maxNestingDepth+2; i++ {
		inner = `{"process":"plot-geometry-prepare","inputs":{"geometry":` + inner + `},"outputs":{"geometry":{}}}`
	}
	body := `{"inputs":{"geometry":` + inner + `},"outputs":{"bbox":{}}}`

	w := executeRequest(ph, "plot-bbox", body, "respond-sync")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-deep workflow, got %d: %s", w.Code, w.Body.String())
	}
	if d := problemDetail(t, w.Body.Bytes()); !strings.Contains(d, "deep") {
		t.Errorf("expected a depth error, got %q", d)
	}
}

// TestWorkflow_NestedFailureKeepsStatus: a nested step that fails on its input
// reports 400, and names which process failed.
func TestWorkflow_NestedFailureKeepsStatus(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"geometry":{
		"process":"plot-geometry-prepare",
		"inputs":{"geometry":{"type":"Polygon","coordinates":[]}},
		"outputs":{"geometry":{}}
	}},"outputs":{"bbox":{}}}`

	w := executeRequest(ph, "plot-bbox", body, "respond-sync")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if d := problemDetail(t, w.Body.Bytes()); !strings.Contains(d, "plot-geometry-prepare") {
		t.Errorf("the error should name the failing nested process, got %q", d)
	}
}

// TestWorkflow_PlainRequestUnaffected guards the Part 1 path: resolution must be
// a no-op when nothing is nested.
func TestWorkflow_PlainRequestUnaffected(t *testing.T) {
	ph := newProcessHandlers()
	body := `{"inputs":{"geometry":` + wfGeometry + `},"outputs":{"bbox":{}}}`
	w := executeRequest(ph, "plot-bbox", body, "respond-sync")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var bbox []float64
	if err := json.Unmarshal(w.Body.Bytes(), &bbox); err != nil || len(bbox) != 4 {
		t.Fatalf("expected a 4-number bbox, got %s", w.Body.String())
	}
}

// TestWorkflow_InputObjectWithProcessField: only an object whose "process" is a
// string is a nested block, so ordinary data is not mistaken for one.
func TestWorkflow_InputObjectWithProcessField(t *testing.T) {
	if _, ok := isNestedProcess(map[string]any{"process": 42}); ok {
		t.Error("a non-string process field should not mark a nested process")
	}
	if _, ok := isNestedProcess(map[string]any{"type": "Polygon"}); ok {
		t.Error("an ordinary geometry should not be a nested process")
	}
	if _, ok := isNestedProcess(map[string]any{"process": "plot-bbox"}); !ok {
		t.Error("an object with a string process field should be a nested process")
	}
}

func TestWorkflow_ConformanceDeclaresNestedProcesses(t *testing.T) {
	want := conformanceP3Prefix + "nested-processes"
	var found bool
	for _, c := range conformanceClasses() {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("/conformance should declare %q, got %v", want, conformanceClasses())
	}
}
