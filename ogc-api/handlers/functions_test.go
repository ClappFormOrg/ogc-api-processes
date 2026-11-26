package handlers

import (
	"ogc-api/models"
	"testing"
)

// ---------- checkType ----------

func TestCheckType_String_Valid(t *testing.T) {
	if err := checkType("x", "string", "hello"); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_String_Invalid(t *testing.T) {
	if err := checkType("x", "string", 42.0); err == "" {
		t.Error("expected error for non-string value")
	}
}

func TestCheckType_Integer_ValidFloat64(t *testing.T) {
	if err := checkType("x", "integer", float64(3)); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Integer_ValidInt(t *testing.T) {
	if err := checkType("x", "integer", int(3)); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Integer_Invalid(t *testing.T) {
	if err := checkType("x", "integer", "three"); err == "" {
		t.Error("expected error for string as integer")
	}
}

func TestCheckType_Number_Valid(t *testing.T) {
	if err := checkType("x", "number", 3.14); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Number_Invalid(t *testing.T) {
	if err := checkType("x", "number", true); err == "" {
		t.Error("expected error for bool as number")
	}
}

func TestCheckType_Boolean_Valid(t *testing.T) {
	if err := checkType("x", "boolean", true); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Boolean_Invalid(t *testing.T) {
	if err := checkType("x", "boolean", "true"); err == "" {
		t.Error("expected error for string as boolean")
	}
}

func TestCheckType_Array_Valid(t *testing.T) {
	if err := checkType("x", "array", []any{"a", "b"}); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Array_Invalid(t *testing.T) {
	if err := checkType("x", "array", "notarray"); err == "" {
		t.Error("expected error for string as array")
	}
}

func TestCheckType_Object_Valid(t *testing.T) {
	if err := checkType("x", "object", map[string]any{"k": "v"}); err != "" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckType_Object_Invalid(t *testing.T) {
	if err := checkType("x", "object", "notobject"); err == "" {
		t.Error("expected error for string as object")
	}
}

func TestCheckType_UnknownType_Passes(t *testing.T) {
	// Unknown schema types should not produce errors.
	if err := checkType("x", "custom", anything()); err != "" {
		t.Errorf("unexpected error for unknown type: %s", err)
	}
}

func anything() any { return struct{}{} }

// ---------- validateInputs ----------

func descWithMessage(minOccurs int) models.ProcessDescription {
	return models.ProcessDescription{
		Inputs: map[string]models.InputDescription{
			"message": {
				Schema:    map[string]string{"type": "string"},
				MinOccurs: minOccurs,
			},
		},
	}
}

func TestValidateInputs_Valid(t *testing.T) {
	errs := validateInputs(descWithMessage(1), map[string]any{"message": "hello"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateInputs_MissingRequired(t *testing.T) {
	errs := validateInputs(descWithMessage(1), map[string]any{})
	if len(errs) == 0 {
		t.Error("expected error for missing required input")
	}
}

func TestValidateInputs_OptionalAbsent(t *testing.T) {
	// MinOccurs=0 means optional; absence is fine.
	errs := validateInputs(descWithMessage(0), map[string]any{})
	if len(errs) != 0 {
		t.Errorf("expected no errors for absent optional input, got %v", errs)
	}
}

func TestValidateInputs_WrongType(t *testing.T) {
	errs := validateInputs(descWithMessage(0), map[string]any{"message": 123.0})
	if len(errs) == 0 {
		t.Error("expected error for wrong input type")
	}
}

func TestValidateInputs_NilInputs(t *testing.T) {
	// A nil inputs map should not panic and should report missing required fields.
	errs := validateInputs(descWithMessage(1), nil)
	if len(errs) == 0 {
		t.Error("expected error for nil inputs with required field")
	}
}

func TestValidateInputs_NoSchemaType_Passes(t *testing.T) {
	desc := models.ProcessDescription{
		Inputs: map[string]models.InputDescription{
			"data": {Schema: nil},
		},
	}
	errs := validateInputs(desc, map[string]any{"data": 42.0})
	if len(errs) != 0 {
		t.Errorf("expected no errors when schema has no type, got %v", errs)
	}
}

// ---------- supportsMode ----------

func descWithModes(modes ...string) models.ProcessDescription {
	return models.ProcessDescription{
		ProcessSummary: models.ProcessSummary{
			JobControlOptions: modes,
		},
	}
}

func TestSupportsMode_Found(t *testing.T) {
	if !supportsMode(descWithModes("sync-execute", "async-execute"), "async-execute") {
		t.Error("expected async-execute to be supported")
	}
}

func TestSupportsMode_NotFound(t *testing.T) {
	if supportsMode(descWithModes("sync-execute"), "async-execute") {
		t.Error("expected async-execute to not be supported")
	}
}

func TestSupportsMode_EmptyList(t *testing.T) {
	if supportsMode(descWithModes(), "sync-execute") {
		t.Error("expected no mode supported on empty list")
	}
}

// ---------- resolveExecutionMode ----------

var bothModes = descWithModes("sync-execute", "async-execute")
var syncOnly = descWithModes("sync-execute")

func TestResolveExecutionMode_RespondAsync(t *testing.T) {
	async, ok := resolveExecutionMode("respond-async", bothModes)
	if !ok || !async {
		t.Errorf("expected async=true ok=true, got async=%v ok=%v", async, ok)
	}
}

func TestResolveExecutionMode_RespondSync(t *testing.T) {
	async, ok := resolveExecutionMode("respond-sync", bothModes)
	if !ok || async {
		t.Errorf("expected async=false ok=true, got async=%v ok=%v", async, ok)
	}
}

// Per OGC 18-062r2 §7.10 Table 14: absent Prefer header defaults to sync when supported.
func TestResolveExecutionMode_EmptyDefaultsToSync_WhenBothSupported(t *testing.T) {
	async, ok := resolveExecutionMode("", bothModes)
	if !ok || async {
		t.Errorf("expected async=false (sync default) when both modes supported, got async=%v ok=%v", async, ok)
	}
}

func TestResolveExecutionMode_EmptyDefaultsToSync_WhenSyncOnly(t *testing.T) {
	async, ok := resolveExecutionMode("", syncOnly)
	if !ok || async {
		t.Errorf("expected async=false when process is sync-only, got async=%v ok=%v", async, ok)
	}
}

var asyncOnly = descWithModes("async-execute")

func TestResolveExecutionMode_EmptyDefaultsToAsync_WhenAsyncOnly(t *testing.T) {
	async, ok := resolveExecutionMode("", asyncOnly)
	if !ok || !async {
		t.Errorf("expected async=true when process is async-only, got async=%v ok=%v", async, ok)
	}
}

// Unknown preferences are ignored (RFC 7240), so they fall back to the default.
func TestResolveExecutionMode_UnknownPreferenceIgnored(t *testing.T) {
	async, ok := resolveExecutionMode("respond-magic", bothModes)
	if !ok || async {
		t.Errorf("expected sync default, got async=%v ok=%v", async, ok)
	}
}

// /req/core/process-execute-auto-execution-mode: an unsupported preference
// falls back to the supported mode instead of failing.
func TestResolveExecutionMode_Fallback(t *testing.T) {
	if async, ok := resolveExecutionMode("respond-async", syncOnly); !ok || async {
		t.Errorf("respond-async on sync-only: expected sync, got async=%v ok=%v", async, ok)
	}
	if async, ok := resolveExecutionMode("respond-sync", asyncOnly); !ok || !async {
		t.Errorf("respond-sync on async-only: expected async, got async=%v ok=%v", async, ok)
	}
	if _, ok := resolveExecutionMode("", descWithModes()); ok {
		t.Error("expected ok=false when the process advertises no execution mode")
	}
}

// ---------- validateSchema ----------

func TestValidateSchema(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []string{"id"},
		"properties": map[string]any{
			"id":    map[string]any{"type": "string"},
			"year":  map[string]any{"type": []string{"integer", "null"}},
			"kind":  map[string]any{"type": "string", "enum": []string{"a", "b"}},
			"ratio": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	valid := map[string]any{"id": "x", "year": nil, "kind": "a", "ratio": 0.5, "tags": []any{"t"}}
	if errs := validateSchema("in", schema, valid); len(errs) != 0 {
		t.Errorf("expected valid, got %v", errs)
	}
	invalid := []map[string]any{
		{"year": 2020.0},                // missing id
		{"id": "x", "year": 2020.5},     // fractional integer
		{"id": "x", "kind": "c"},        // enum
		{"id": "x", "ratio": 2.0},       // maximum
		{"id": "x", "tags": []any{1.0}}, // item type
	}
	for _, v := range invalid {
		if errs := validateSchema("in", schema, v); len(errs) == 0 {
			t.Errorf("expected errors for %v", v)
		}
	}
}

// ---------- runProcess ----------

func TestRunProcess_KnownProcess(t *testing.T) {
	if processFunctions == nil {
		processFunctions = map[string]ProcessFunc{}
	}
	processFunctions["test"] = func(inputs map[string]any) (map[string]any, error) {
		return map[string]any{"result": inputs["value"]}, nil
	}

	m, err := runProcess("test", map[string]any{"value": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["result"] != "test" {
		t.Errorf("expected 'test', got %v", m["result"])
	}
}

func TestRunProcess_UnknownProcess(t *testing.T) {
	if processFunctions == nil {
		processFunctions = map[string]ProcessFunc{}
	}
	_, err := runProcess("nonexistent", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown process")
	}
	if errorStatus(err) != 500 {
		t.Errorf("expected status 500, got %d", errorStatus(err))
	}
}

func TestErrorStatus_ProcessError(t *testing.T) {
	if got := errorStatus(upstreamError("down")); got != 502 {
		t.Errorf("expected 502 for upstream error, got %d", got)
	}
}

// ---------- Prefer parsing ----------

func TestResolveExecutionMode_PreferList(t *testing.T) {
	// RFC 7240: several comma-separated preferences, with parameters.
	async, ok := resolveExecutionMode("return=representation, respond-async; wait=10", bothModes)
	if !ok || !async {
		t.Errorf("expected async=true ok=true, got async=%v ok=%v", async, ok)
	}
}

func TestAppliedPreference(t *testing.T) {
	if got := appliedPreference("respond-async", true); got != "respond-async" {
		t.Errorf("expected respond-async, got %q", got)
	}
	if got := appliedPreference("respond-async", false); got != "" {
		t.Errorf("expected no applied preference when downgraded, got %q", got)
	}
	if got := appliedPreference("", false); got != "" {
		t.Errorf("expected no applied preference without header, got %q", got)
	}
}

// ---------- normalizeInputs ----------

func TestNormalizeInputs(t *testing.T) {
	inputs := map[string]any{
		"qualified": map[string]any{"value": 5.0, "mediaType": "application/json"},
		"object":    map[string]any{"value": 1.0, "unit": "m"}, // not a qualified value
		"array":     []any{map[string]any{"value": "a"}, "b"},
		"bare":      "x",
	}
	normalizeInputs(inputs)
	if inputs["qualified"] != 5.0 {
		t.Errorf("qualified value not unwrapped: %v", inputs["qualified"])
	}
	if _, ok := inputs["object"].(map[string]any); !ok {
		t.Errorf("ordinary object should be left alone: %v", inputs["object"])
	}
	if arr := inputs["array"].([]any); arr[0] != "a" || arr[1] != "b" {
		t.Errorf("array elements not unwrapped: %v", arr)
	}
	if inputs["bare"] != "x" {
		t.Errorf("bare value changed: %v", inputs["bare"])
	}
}
