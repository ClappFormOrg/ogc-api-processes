package handlers

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"ogc-api/models"
	"ogc-api/processes/plotbag"
	"ogc-api/processes/plotbag/pdok"
	"ogc-api/store"
	"slices"
	"sort"
	"strings"
)

// ProcessFunc is the signature for all process implementations. It returns the
// outputs keyed by output ID. A returned *ProcessError carries the HTTP status
// that describes the failure; any other error is reported as 500.
type ProcessFunc func(inputs map[string]any) (map[string]any, error)

// ProcessError is a process failure with the HTTP status that describes it.
type ProcessError struct {
	Status int
	Msg    string
}

func (e *ProcessError) Error() string { return e.Msg }

// upstreamError reports a failure of an upstream service (502 Bad Gateway).
func upstreamError(msg string) error {
	return &ProcessError{Status: http.StatusBadGateway, Msg: msg}
}

// errorStatus returns the HTTP status for a process error.
func errorStatus(err error) int {
	var pe *ProcessError
	if errors.As(err, &pe) && pe.Status != 0 {
		return pe.Status
	}
	return http.StatusInternalServerError
}

// ProcessDefinition bundles a process description with its implementation.
// Add new processes to the processRegistry slice below.
type ProcessDefinition struct {
	Description models.ProcessDescription
	Fn          ProcessFunc
}

// processRegistry is the single source of truth for all processes.
// Add a new ProcessDefinition here to register both its metadata and implementation.
var processRegistry = []ProcessDefinition{
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "plot-bag-matching",
				Title: "Kavel-BAG matching",
				Description: "Matches a housing-plan plot polygon against BAG buildings within the search area and returns " +
					"the matched BAG IDs with an overlap/confidence score. Plots without a (full) match are flagged.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "plancapaciteit", "woningbouw", "matching", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"plot": {
					Title:       "Plot",
					Description: "Plot identifier, geometry (WGS84 GeoJSON Polygon/MultiPolygon) and plan status.",
					Schema:      plotbag.PlotSchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
				"bag_buildings": {
					Title: "BAG buildings",
					Description: "BAG panden/verblijfsobjecten within the plot's search area as WGS84 GeoJSON Features, " +
						"already fetched by the caller (e.g. via an OGC API Features proxy on PDOK BAG).",
					Schema:    plotbag.BAGBuildingsSchema,
					MaxOccurs: 1,
				},
				"min_overlap_ratio": {
					Title:       "Minimum overlap ratio",
					Description: "Minimum fraction of a building inside the plot for it to count as a candidate (default: 0.05).",
					Schema:      plotbag.MinOverlapRatioSchema,
					MaxOccurs:   1,
				},
				"full_match_threshold": {
					Title:       "Full match threshold",
					Description: "Overlap ratio from which a building counts as fully matched (default: 0.95).",
					Schema:      plotbag.FullMatchThresholdSchema,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"match_result": {
					Title:       "Match result",
					Description: "Matched BAG IDs for the plot with overlap score, plus a match summary and flag.",
					Schema:      plotbag.MatchResultSchema,
				},
			},
		},
		Fn: plotBagMatching,
	},
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "plot-bag-matching-pdok",
				Title: "Kavel-BAG matching (BAG from PDOK)",
				Description: "Same matching as plot-bag-matching, but the BAG buildings are not supplied by the " +
					"caller: the process fetches the panden intersecting the plot's bounding box from the public " +
					"PDOK BAG WFS (bag:pand, no API key needed). Only panden are queried - PDOK serves " +
					"verblijfsobjecten as points, which cannot have an area overlap.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "PDOK", "plancapaciteit", "woningbouw", "matching", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"plot": {
					Title:       "Plot",
					Description: "Plot identifier, geometry (WGS84 GeoJSON Polygon/MultiPolygon) and plan status.",
					Schema:      plotbag.PlotSchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
				"min_overlap_ratio": {
					Title:       "Minimum overlap ratio",
					Description: "Minimum fraction of a building inside the plot for it to count as a candidate (default: 0.05).",
					Schema:      plotbag.MinOverlapRatioSchema,
					MaxOccurs:   1,
				},
				"full_match_threshold": {
					Title:       "Full match threshold",
					Description: "Overlap ratio from which a building counts as fully matched (default: 0.95).",
					Schema:      plotbag.FullMatchThresholdSchema,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"match_result": {
					Title:       "Match result",
					Description: "Matched BAG IDs for the plot with overlap score, plus a match summary and flag.",
					Schema:      plotbag.MatchResultSchema,
				},
				"bag_query": {
					Title:       "BAG query",
					Description: "What was fetched from PDOK: endpoint, collection, bounding box, feature and request counts.",
					Schema:      pdok.BAGQuerySchema,
				},
			},
		},
		Fn: plotBagMatchingPDOK,
	},

	// ---- plot-bag-matching as separate steps -------------------------------
	// The five processes below are the stages of plot-bag-matching-pdok, each
	// executable on its own so a recipe can chain them, inspect what every
	// stage produced and replace one. They share their implementation with the
	// single-call processes above (see processes/plotbag/steps.go), so the two
	// routes cannot drift apart. Chaining all five reproduces
	// plot-bag-matching-pdok; skipping steps 2 and 3 reproduces
	// plot-bag-matching.
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "plot-geometry-prepare",
				Title: "Step 1 - Prepare plot geometry",
				Description: "Reads the plot geometry, rejects it if it is empty or unusable, repairs " +
					"self-intersections where possible, and reports what it found: whether the input was " +
					"already valid, whether it had to be repaired, its area in square metres and its " +
					"bounding box. Geometry comes back as WGS84 GeoJSON, ready for the next step.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "geometry", "validation", "step", "recipe", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"geometry": {
					Title:       "Plot geometry",
					Description: "WGS84 GeoJSON Polygon or MultiPolygon. A whole plot object may be passed as plot instead.",
					Schema:      plotbag.GeometrySchema,
					MaxOccurs:   1,
				},
				"plot": {
					Title:       "Plot",
					Description: "Alternative to geometry: a plot object whose geometry member is used.",
					Schema:      plotbag.PlotSchema,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"geometry": {
					Title:       "Prepared geometry",
					Description: "The validated and, where needed, repaired geometry as WGS84 GeoJSON.",
					Schema:      plotbag.GeometrySchema,
				},
				"geometry_report": {
					Title:       "Geometry report",
					Description: "Whether the geometry was valid, whether it was repaired, its ring and polygon counts, area and bounding box.",
					Schema:      plotbag.GeometryReportSchema,
				},
			},
		},
		Fn: plotGeometryPrepare,
	},
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "plot-bbox",
				Title: "Step 2 - Bounding box of a geometry",
				Description: "Returns the WGS84 bounding box of a GeoJSON Polygon or MultiPolygon as " +
					"[minLon, minLat, maxLon, maxLat]. This is the extent bag-panden-fetch queries: every " +
					"building that overlaps the plot also overlaps its bounding box, so no candidate is lost.",
				Version:           "0.1.0",
				Keywords:          []string{"geometry", "bbox", "extent", "step", "recipe", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"geometry": {
					Title:       "Geometry",
					Description: "WGS84 GeoJSON Polygon or MultiPolygon, typically the output of plot-geometry-prepare.",
					Schema:      plotbag.GeometrySchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"bbox": {
					Title:       "Bounding box",
					Description: "[minLon, minLat, maxLon, maxLat] in WGS84.",
					Schema:      plotbag.BBoxSchema,
				},
			},
		},
		Fn: plotBBox,
	},
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "bag-panden-fetch",
				Title: "Step 3 - Fetch BAG panden for a bounding box",
				Description: "Fetches the BAG panden intersecting a WGS84 bounding box from the public " +
					"PDOK BAG WFS (bag:pand, no API key needed). Features are paged out 1000 at a time and " +
					"capped at 20000 per execution; hitting the cap sets truncated on bag_query. Run as its " +
					"own step, the features can be fetched once and reused across several matching runs.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "PDOK", "WFS", "step", "recipe", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"bbox": {
					Title:       "Bounding box",
					Description: "[minLon, minLat, maxLon, maxLat] in WGS84, typically the output of plot-bbox.",
					Schema:      plotbag.BBoxSchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"bag_buildings": {
					Title:       "BAG buildings",
					Description: "The fetched panden as GeoJSON features in CRS84.",
					Schema:      plotbag.BAGBuildingsSchema,
				},
				"bag_query": {
					Title:       "BAG query",
					Description: "What was asked of PDOK and what came back: endpoint, collection, bounding box, feature and request counts.",
					Schema:      pdok.BAGQuerySchema,
				},
			},
		},
		Fn: bagPandenFetch,
	},
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "plot-bag-overlap",
				Title: "Step 4 - Overlap of BAG buildings with the plot",
				Description: "Computes, for each BAG building, the share of its area that lies inside the " +
					"plot, and keeps the ones at or above min_overlap_ratio. Overlap is measured in RD New " +
					"(EPSG:28992). Nothing is classified as a full or partial match here - that is step 5 - " +
					"so the thresholds can be changed without recomputing any geometry. overlap_ratio is " +
					"returned unrounded, which is what lets the chained steps match plot-bag-matching exactly.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "overlap", "geometry", "step", "recipe", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"geometry": {
					Title:       "Plot geometry",
					Description: "WGS84 GeoJSON Polygon or MultiPolygon, typically the output of plot-geometry-prepare.",
					Schema:      plotbag.GeometrySchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
				"bag_buildings": {
					Title:       "BAG buildings",
					Description: "GeoJSON features with properties.identificatie, typically the output of bag-panden-fetch.",
					Schema:      plotbag.BAGBuildingsSchema,
					MaxOccurs:   1,
				},
				"min_overlap_ratio": {
					Title:       "Minimum overlap ratio",
					Description: "Minimum fraction of a building inside the plot for it to be kept (default: 0.05).",
					Schema:      plotbag.MinOverlapRatioSchema,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"overlaps": {
					Title:       "Overlaps",
					Description: "One entry per building above the threshold, with its unrounded overlap ratio and the areas it was computed from.",
					Schema:      plotbag.OverlapsSchema,
				},
				"skipped": {
					Title:       "Skipped features",
					Description: "Counts of BAG features that could not be used, which an empty overlaps list alone does not show.",
					Schema:      plotbag.SkippedSchema,
				},
			},
		},
		Fn: plotBagOverlap,
	},
	{
		Description: models.ProcessDescription{
			ProcessSummary: models.ProcessSummary{
				ID:    "bag-match-classify",
				Title: "Step 5 - Classify overlaps into a match result",
				Description: "Turns a list of overlaps into the match_result of plot-bag-matching: sorts " +
					"them, splits them into full and partial matches at full_match_threshold, counts them " +
					"and sets the summary flag and reason. Because it takes overlaps rather than geometry, " +
					"a recipe can re-run it at several thresholds without touching PDOK or recomputing any area.",
				Version:           "0.1.0",
				Keywords:          []string{"BAG", "matching", "classification", "step", "recipe", "nldt"},
				JobControlOptions: []string{"sync-execute", "async-execute", "dismiss"},
			},
			Inputs: map[string]models.InputDescription{
				"overlaps": {
					Title:       "Overlaps",
					Description: "The overlaps output of plot-bag-overlap.",
					Schema:      plotbag.OverlapsSchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
				"plan_dataid": {
					Title:       "Plan data ID",
					Description: "Plot identifier echoed into the match result.",
					Schema:      plotbag.PlanDataIDSchema,
					MinOccurs:   1,
					MaxOccurs:   1,
				},
				"plan_plannaam": {
					Title:       "Plan name",
					Description: "Optional plan name echoed into the match result.",
					Schema:      plotbag.PlanPlannaamSchema,
					MaxOccurs:   1,
				},
				"full_match_threshold": {
					Title:       "Full match threshold",
					Description: "Overlap ratio from which a building counts as fully matched (default: 0.95).",
					Schema:      plotbag.FullMatchThresholdSchema,
					MaxOccurs:   1,
				},
			},
			Outputs: map[string]models.OutputDescription{
				"match_result": {
					Title:       "Match result",
					Description: "Matched BAG IDs with overlap score and match type, plus the match summary and flag.",
					Schema:      plotbag.MatchResultSchema,
				},
			},
		},
		Fn: bagMatchClassify,
	},
}

// processFunctions and processDescriptions are built from processRegistry at startup.
var processFunctions map[string]ProcessFunc
var processDescriptions map[string]models.ProcessDescription

// NewProcessHandlers creates a ProcessHandlers, registers all process definitions
// into the store, and builds the function and description lookup maps.
// Links are injected here (after BaseURL is set): self, the execute endpoint,
// and the ogc-process-description profile required by 2.0
// (/req/ogc-process-description/json-encoding).
func NewProcessHandlers(s *store.Store) *ProcessHandlers {
	processFunctions = make(map[string]ProcessFunc, len(processRegistry))
	processDescriptions = make(map[string]models.ProcessDescription, len(processRegistry))
	for _, def := range processRegistry {
		desc := def.Description
		desc.Links = []models.Link{
			{
				Href:  BaseURL + "/processes/" + desc.ID,
				Rel:   "self",
				Type:  "application/json",
				Title: desc.Title,
			},
			{
				Href:  BaseURL + "/processes/" + desc.ID + "/execution",
				Rel:   relOGCPrefix + "execute",
				Type:  "application/json",
				Title: "Execute " + desc.ID,
			},
			{
				Href: profileProcessDescription,
				Rel:  "profile",
			},
		}
		// By-reference inputs are not implemented, so say so explicitly
		// (an absent valuePassing means byValue and byReference).
		inputs := make(map[string]models.InputDescription, len(desc.Inputs))
		for name, in := range desc.Inputs {
			if in.ValuePassing == nil {
				in.ValuePassing = []string{"byValue"}
			}
			inputs[name] = in
		}
		desc.Inputs = inputs
		s.RegisterProcess(desc)
		processFunctions[desc.ID] = def.Fn
		processDescriptions[desc.ID] = desc
	}
	return &ProcessHandlers{Store: s}
}

// validateInputs checks that all required inputs are present and that every
// provided input validates against its schema (/req/core/process-execute-input-validation).
// Returns a slice of human-readable error messages; empty means valid.
func validateInputs(desc models.ProcessDescription, inputs map[string]any) []string {
	var errs []string

	for name, input := range desc.Inputs {
		val, provided := inputs[name]

		if input.MinOccurs > 0 && !provided {
			errs = append(errs, fmt.Sprintf("missing required input: %q", name))
			continue
		}
		if !provided {
			continue
		}
		if isReference(val) {
			// Every input advertises valuePassing: ["byValue"].
			errs = append(errs, fmt.Sprintf("input %q: by-reference values (href) are not supported", name))
			continue
		}
		errs = append(errs, validateSchema(name, input.Schema, val)...)
	}

	return errs
}

// isReference reports whether v is a link ({"href": …}) rather than an inline value.
func isReference(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, hasHref := m["href"]
	return hasHref && len(m) <= 4 // href plus optional rel/type/title
}

// validateSchema validates val against the subset of JSON Schema 2020-12 the
// process descriptions use: type (string or array, incl. "null"), enum,
// minimum/maximum, required, properties, items and oneOf. Unknown keywords
// are ignored, so an unsupported constraint never rejects a valid request.
func validateSchema(path string, schema, val any) []string {
	s := schemaMap(schema)
	if s == nil {
		return nil
	}

	if types := schemaTypes(s["type"]); len(types) > 0 {
		var firstErr string
		matched := false
		for _, t := range types {
			if t == "null" {
				if val == nil {
					matched = true
					break
				}
				continue
			}
			if err := checkType(path, t, val); err == "" {
				matched = true
				break
			} else if firstErr == "" {
				firstErr = err
			}
		}
		if !matched {
			if firstErr == "" || len(types) > 1 {
				firstErr = fmt.Sprintf("input %q must be of type %s", path, strings.Join(types, " or "))
			}
			return []string{firstErr}
		}
	}

	var errs []string
	if enum := toAnySlice(s["enum"]); enum != nil && !slices.ContainsFunc(enum, func(e any) bool { return jsonEqual(e, val) }) {
		errs = append(errs, fmt.Sprintf("input %q must be one of %v", path, enum))
	}
	if n, ok := toFloat(val); ok {
		if lo, ok := toFloat(s["minimum"]); ok && n < lo {
			errs = append(errs, fmt.Sprintf("input %q must be >= %v", path, s["minimum"]))
		}
		if hi, ok := toFloat(s["maximum"]); ok && n > hi {
			errs = append(errs, fmt.Sprintf("input %q must be <= %v", path, s["maximum"]))
		}
	}
	if obj, ok := val.(map[string]any); ok {
		for _, r := range toAnySlice(s["required"]) {
			if key, _ := r.(string); key != "" {
				if _, present := obj[key]; !present {
					errs = append(errs, fmt.Sprintf("input %q is missing required property %q", path, key))
				}
			}
		}
		if props, ok := s["properties"].(map[string]any); ok {
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if v, present := obj[k]; present {
					errs = append(errs, validateSchema(path+"."+k, props[k], v)...)
				}
			}
		}
	}
	if arr, ok := val.([]any); ok && s["items"] != nil {
		for i, v := range arr {
			errs = append(errs, validateSchema(fmt.Sprintf("%s[%d]", path, i), s["items"], v)...)
		}
	}
	if oneOf := toAnySlice(s["oneOf"]); oneOf != nil {
		valid := 0
		for _, sub := range oneOf {
			if len(validateSchema(path, sub, val)) == 0 {
				valid++
			}
		}
		if valid != 1 {
			errs = append(errs, fmt.Sprintf("input %q must match exactly one of the allowed schemas", path))
		}
	}
	return errs
}

// schemaMap normalises a registry schema literal to map[string]any.
func schemaMap(schema any) map[string]any {
	switch s := schema.(type) {
	case map[string]any:
		return s
	case map[string]string:
		m := make(map[string]any, len(s))
		for k, v := range s {
			m[k] = v
		}
		return m
	}
	return nil
}

func schemaTypes(t any) []string {
	switch v := t.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func toAnySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// jsonEqual compares a schema literal with a decoded JSON value.
func jsonEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		fb, ok := toFloat(b)
		return ok && fa == fb
	}
	return a == b
}

// checkType validates that val matches the expected JSON schema type.
// Returns an error message, or "" if valid.
func checkType(name, schemaType string, val any) string {
	switch schemaType {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Sprintf("input %q must be a string", name)
		}
	case "integer":
		switch n := val.(type) {
		case float64:
			// JSON numbers decode as float64; reject fractional values.
			if n != math.Trunc(n) {
				return fmt.Sprintf("input %q must be an integer", name)
			}
		case int, int64:
		default:
			return fmt.Sprintf("input %q must be an integer", name)
		}
	case "number":
		switch val.(type) {
		case float64, int, int64:
		default:
			return fmt.Sprintf("input %q must be a number", name)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("input %q must be a boolean", name)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Sprintf("input %q must be an array", name)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Sprintf("input %q must be an object", name)
		}
	}
	return ""
}

// plotBagMatching runs plotbag.Run, reporting invalid input as 400.
func plotBagMatching(inputs map[string]any) (map[string]any, error) {
	out, err := plotbag.Run(inputs)
	var ie *plotbag.InputError
	if errors.As(err, &ie) {
		return nil, &ProcessError{Status: http.StatusBadRequest, Msg: ie.Msg}
	}
	return out, err
}

// plotBagMatchingPDOK runs pdok.Run, reporting invalid input as 400 and a
// failure of the PDOK service as 502.
func plotBagMatchingPDOK(inputs map[string]any) (map[string]any, error) {
	out, err := pdok.Run(inputs)
	var ie *plotbag.InputError
	if errors.As(err, &ie) {
		return nil, &ProcessError{Status: http.StatusBadRequest, Msg: ie.Msg}
	}
	var ue *pdok.UpstreamError
	if errors.As(err, &ue) {
		return nil, upstreamError(ue.Msg)
	}
	return out, err
}

// ---- step processes -------------------------------------------------------
// Each wrapper maps step errors to HTTP status the same way the whole-pipeline
// processes do: bad input is 400, a PDOK failure is 502.

// stepInputError reports a plotbag input problem as 400.
func stepInputError(err error) error {
	var ie *plotbag.InputError
	if errors.As(err, &ie) {
		return &ProcessError{Status: http.StatusBadRequest, Msg: ie.Msg}
	}
	return err
}

func plotGeometryPrepare(inputs map[string]any) (map[string]any, error) {
	out, err := plotbag.RunPrepareGeometry(inputs)
	return out, stepInputError(err)
}

func plotBBox(inputs map[string]any) (map[string]any, error) {
	out, err := plotbag.RunBBox(inputs)
	return out, stepInputError(err)
}

func bagPandenFetch(inputs map[string]any) (map[string]any, error) {
	out, err := pdok.RunFetch(inputs)
	var ue *pdok.UpstreamError
	if errors.As(err, &ue) {
		return nil, upstreamError(ue.Msg)
	}
	return out, stepInputError(err)
}

func plotBagOverlap(inputs map[string]any) (map[string]any, error) {
	out, err := plotbag.RunOverlaps(inputs)
	return out, stepInputError(err)
}

func bagMatchClassify(inputs map[string]any) (map[string]any, error) {
	out, err := plotbag.RunClassify(inputs)
	return out, stepInputError(err)
}
