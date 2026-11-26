package handlers

// OGC API - Processes - Part 3: Workflows and Chaining - nested-processes.
//
// Part 3 extends the Part 1 execution request so that an input value may itself
// be an execution request: an object carrying a "process" key (inputProcess.yaml
// in the standard). The server executes that process and substitutes its output
// for the input. For the plot-bag steps this collapses the whole recipe into a
// single POST instead of five round trips, with the intermediate values never
// leaving the server.
//
// Part 3 is a Candidate SWG Draft, which is why the conformance URIs below
// carry version 0.0 - they are expected to change before the standard is
// adopted.
//
// Only locally registered processes are resolved. Pointing at a process on
// another server is the separate remote-core-processes class, which is not
// implemented here, so such a reference is rejected rather than quietly fetched.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"ogc-api/models"
)

const (
	// conformanceP3Prefix is the Part 3 conformance class base URI.
	conformanceP3Prefix = "https://www.opengis.net/spec/ogcapi-processes-3/0.0/conf/"

	// maxNestingDepth bounds the recursion. Inputs are resolved depth first, so
	// without a bound a document that nests too far - or one whose processes
	// reference each other in a cycle - would exhaust the stack. A cycle shows
	// up here as a depth overflow rather than a hang.
	maxNestingDepth = 8
)

// workflowError reports a malformed workflow document (400). A failure inside a
// nested process keeps its own status instead, so a PDOK outage in step 3 of a
// chained request still surfaces as 502.
func workflowError(format string, args ...any) error {
	return &ProcessError{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, args...)}
}

// isNestedProcess reports whether a value is a Part 3 nested process block. The
// standard marks one with a "process" key holding a URI reference.
func isNestedProcess(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if _, ok := m["process"].(string); !ok {
		return nil, false
	}
	return m, true
}

// localProcessID turns the "process" URI reference of a nested block into a
// process ID on this server.
//
// A bare ID is a relative reference resolved against this API, which is what
// the standard prescribes: a nested {"process": "plot-bbox"} received by a
// process at {root}/processes/{id} resolves to {root}/processes/plot-bbox. An
// absolute URI is accepted when it points back at this server.
func localProcessID(ref string) (string, error) {
	id := strings.TrimSpace(ref)
	if id == "" {
		return "", workflowError("nested process has an empty 'process' reference")
	}

	if strings.Contains(id, "://") {
		prefix := strings.TrimSuffix(BaseURL, "/") + "/processes/"
		if !strings.HasPrefix(id, prefix) {
			return "", workflowError(
				"nested process %q is on another server; executing remote processes is the "+
					"remote-core-processes conformance class, which this server does not implement", ref)
		}
		id = strings.TrimPrefix(id, prefix)
	}

	// Tolerate a reference written against the local path rather than a bare ID.
	id = strings.TrimPrefix(id, "/processes/")
	id = strings.TrimPrefix(id, "processes/")
	id = strings.Trim(id, "/")

	if id == "" || strings.Contains(id, "/") {
		return "", workflowError("nested process reference %q is not a process on this server", ref)
	}
	return id, nil
}

// nestedOutputName decides which output of a nested process becomes the input
// value. A nested process stands where a single value is expected, so it has to
// resolve to exactly one output.
//
// The block may name it by requesting a single output. Otherwise a process with
// exactly one output is unambiguous and needs no selection. Anything else is a
// malformed document rather than something to guess at.
func nestedOutputName(block map[string]any, id string, desc models.ProcessDescription) (string, error) {
	available := make([]string, 0, len(desc.Outputs))
	for name := range desc.Outputs {
		available = append(available, name)
	}
	sort.Strings(available)

	if raw, present := block["outputs"]; present && raw != nil {
		requested, ok := raw.(map[string]any)
		if !ok {
			return "", workflowError("nested process %q: 'outputs' must be an object", id)
		}
		names := make([]string, 0, len(requested))
		for name := range requested {
			names = append(names, name)
		}
		sort.Strings(names)

		switch len(names) {
		case 1:
			if _, ok := desc.Outputs[names[0]]; !ok {
				return "", workflowError("nested process %q has no output %q (it has: %s)",
					id, names[0], strings.Join(available, ", "))
			}
			return names[0], nil
		case 0:
			// Fall through to the implicit single-output case.
		default:
			return "", workflowError(
				"nested process %q requests %d outputs, but a nested process supplies one input value; "+
					"request exactly one of: %s", id, len(names), strings.Join(available, ", "))
		}
	}

	if len(available) == 1 {
		return available[0], nil
	}
	return "", workflowError(
		"nested process %q has %d outputs, so the one to use must be named in its 'outputs' (one of: %s)",
		id, len(available), strings.Join(available, ", "))
}

// resolveNestedValue replaces any nested process block inside v with the output
// of running it. Arrays are walked because an input may be a list of values, any
// of which can be a nested process.
func resolveNestedValue(v any, apiKey string, depth int) (any, error) {
	if depth > maxNestingDepth {
		return nil, workflowError(
			"workflow nests more than %d processes deep; this is either a very deep workflow or "+
				"processes referencing each other in a cycle", maxNestingDepth)
	}

	if block, ok := isNestedProcess(v); ok {
		return runNestedProcess(block, apiKey, depth)
	}

	if list, ok := v.([]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			resolved, err := resolveNestedValue(item, apiKey, depth)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	}
	return v, nil
}

// runNestedProcess executes one nested process block and returns the single
// output that stands in for the input.
func runNestedProcess(block map[string]any, apiKey string, depth int) (any, error) {
	ref, _ := block["process"].(string)
	id, err := localProcessID(ref)
	if err != nil {
		return nil, err
	}
	desc, ok := processDescriptions[id]
	if !ok {
		return nil, &ProcessError{
			Status: http.StatusBadRequest,
			Msg:    fmt.Sprintf("nested process not found: %s", id),
		}
	}

	inputs := map[string]any{}
	if raw, present := block["inputs"]; present && raw != nil {
		given, ok := raw.(map[string]any)
		if !ok {
			return nil, workflowError("nested process %q: 'inputs' must be an object", id)
		}
		for k, v := range given {
			inputs[k] = v
		}
	}

	// Depth first: a nested process may itself take nested processes.
	normalizeInputs(inputs)
	if err := resolveNestedInputs(inputs, apiKey, depth+1); err != nil {
		return nil, err
	}
	if apiKey != "" {
		inputs["_api_key"] = apiKey
	}

	if errs := validateInputs(desc, inputs); len(errs) > 0 {
		Logger.Warn("invalid nested inputs", "process", id, "errors", errs)
		return nil, workflowError("nested process %q: %s", id, strings.Join(errs, "; "))
	}

	outputName, err := nestedOutputName(block, id, desc)
	if err != nil {
		return nil, err
	}

	Logger.Info("executing nested process", "process", id, "output", outputName, "depth", depth)
	result, err := runProcess(id, inputs)
	if err != nil {
		// Keep the nested failure's own status: a 502 from PDOK deep in a
		// workflow is still a 502, not a bad request.
		Logger.Warn("nested process failed", "process", id, "error", err.Error())
		return nil, &ProcessError{
			Status: errorStatus(err),
			Msg:    fmt.Sprintf("nested process %q failed: %s", id, err.Error()),
		}
	}

	value, ok := result[outputName]
	if !ok {
		return nil, &ProcessError{
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("nested process %q produced no output %q", id, outputName),
		}
	}

	// Round-trip the output through JSON before it becomes an input.
	//
	// Process functions return Go-native values - plot-bbox returns a
	// [4]float64 - while every input parser and validator in this API expects
	// what encoding/json produces: []any, float64, map[string]any. Over HTTP
	// that conversion happens for free between two requests. Resolving in
	// process skips it, so without this a chained bbox arrives as a Go array
	// and is rejected as "must be an array". Marshalling here makes the nested
	// path behave exactly like the chained one, which is the whole point of it
	// producing the same answer. The cost is trivial next to a PDOK fetch.
	return jsonRoundTrip(value, id, outputName)
}

func jsonRoundTrip(value any, id, outputName string) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, &ProcessError{
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("nested process %q: output %q could not be encoded: %v", id, outputName, err),
		}
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, &ProcessError{
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("nested process %q: output %q could not be decoded: %v", id, outputName, err),
		}
	}
	return decoded, nil
}

// resolveNestedInputs replaces every nested process block in inputs with the
// output of executing it, in place. It is a no-op for an ordinary Part 1
// request, so it is safe to call on every execution.
func resolveNestedInputs(inputs map[string]any, apiKey string, depth int) error {
	// Sorted so that a workflow with several nested branches executes in a
	// stable order, which keeps logs and tests reproducible.
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		resolved, err := resolveNestedValue(inputs[name], apiKey, depth)
		if err != nil {
			return err
		}
		inputs[name] = resolved
	}
	return nil
}

// hasNestedProcess reports whether a request carries any nested process, used
// only to keep the log quiet for ordinary Part 1 executions.
func hasNestedProcess(inputs map[string]any) bool {
	for _, v := range inputs {
		if _, ok := isNestedProcess(v); ok {
			return true
		}
		if list, ok := v.([]any); ok {
			for _, item := range list {
				if _, ok := isNestedProcess(item); ok {
					return true
				}
			}
		}
	}
	return false
}
