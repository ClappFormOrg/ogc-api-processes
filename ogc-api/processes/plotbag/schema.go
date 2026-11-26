package plotbag

// JSON Schemas for the process description, taken from PROCESS_METADATA in the
// notebook. Nullable fields use JSON Schema type arrays (["integer", "null"]),
// the dialect used by OGC API - Processes 2.0 process descriptions.

var geometrySchema = map[string]any{
	"type":     "object",
	"required": []string{"type", "coordinates"},
	"properties": map[string]any{
		"type":        map[string]any{"type": "string", "enum": []string{"Polygon", "MultiPolygon"}},
		"coordinates": map[string]any{"type": "array"},
	},
}

// featureSchema is deliberately looser than geometrySchema: GeoJSON allows a
// null geometry or properties, and the process skips (and logs) buildings it
// cannot use rather than rejecting the whole request, as the notebook did.
var featureSchema = map[string]any{
	"type":     "object",
	"required": []string{"type", "geometry", "properties"},
	"properties": map[string]any{
		"type":     map[string]any{"type": "string", "enum": []string{"Feature"}},
		"geometry": map[string]any{"type": []string{"object", "null"}},
		"properties": map[string]any{
			"type": []string{"object", "null"},
			"properties": map[string]any{
				"identificatie": map[string]any{"type": "string"},
				"bouwjaar":      map[string]any{"type": []string{"integer", "null"}},
				"gebruiksdoel": map[string]any{
					"oneOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						map[string]any{"type": "null"},
					},
				},
			},
		},
	},
}

// PlotSchema is the schema of the "plot" input.
var PlotSchema = map[string]any{
	"type":     "object",
	"required": []string{"plan_dataid", "geometry"},
	"properties": map[string]any{
		"plan_dataid":          map[string]any{"type": "string"},
		"plan_plannaam":        map[string]any{"type": "string"},
		"gemeente":             map[string]any{"type": "string"},
		"geometry":             geometrySchema,
		"planned_bruto":        map[string]any{"type": "integer", "minimum": 0},
		"planned_netto":        map[string]any{"type": "integer", "minimum": 0},
		"planologische_status": map[string]any{"type": "string", "enum": []string{"zacht", "hard"}},
	},
}

// BAGBuildingsSchema is the schema of the "bag_buildings" input.
var BAGBuildingsSchema = map[string]any{"type": "array", "items": featureSchema}

// MinOverlapRatioSchema is the schema of the "min_overlap_ratio" input.
var MinOverlapRatioSchema = map[string]any{
	"type": "number", "minimum": 0, "maximum": 1, "default": DefaultMinOverlapRatio,
}

// FullMatchThresholdSchema is the schema of the "full_match_threshold" input.
var FullMatchThresholdSchema = map[string]any{
	"type": "number", "minimum": 0, "maximum": 1, "default": DefaultFullMatchThreshold,
}

// MatchResultSchema is the schema of the "match_result" output.
var MatchResultSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"plan_dataid":   map[string]any{"type": "string"},
		"plan_plannaam": map[string]any{"type": []string{"string", "null"}},
		"matches": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"bag_id":        map[string]any{"type": "string"},
					"bouwjaar":      map[string]any{"type": []string{"integer", "null"}},
					"overlap_ratio": map[string]any{"type": "number"},
					"score":         map[string]any{"type": "number"},
					"match_type":    map[string]any{"type": "string", "enum": []string{MatchTypeFull, MatchTypePartial}},
				},
			},
		},
		"summary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"n_candidates":      map[string]any{"type": "integer"},
				"n_full_matches":    map[string]any{"type": "integer"},
				"n_partial_matches": map[string]any{"type": "integer"},
				"fully_matched":     map[string]any{"type": "boolean"},
				"flagged":           map[string]any{"type": "boolean"},
				"flag_reason": map[string]any{
					"type": []string{"string", "null"},
					"enum": []any{FlagNoBAGObject, FlagPartialOnly, nil},
				},
			},
		},
	},
}

// ---------------------------------------------------------- step schemas --
// Schemas for the separately executable steps. The step processes exist so a
// recipe can run the pipeline one stage at a time; see steps.go.

// GeometrySchema is the schema of the standalone "geometry" input, the plot
// geometry on its own rather than wrapped in a plot object.
var GeometrySchema = geometrySchema

// GeometryReportSchema is the schema of the geometry_report output.
var GeometryReportSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"valid":      map[string]any{"type": "boolean"},
		"repaired":   map[string]any{"type": "boolean"},
		"n_polygons": map[string]any{"type": "integer"},
		"n_rings":    map[string]any{"type": "integer"},
		"area_m2":    map[string]any{"type": "number"},
		"bbox":       BBoxSchema,
	},
}

// BBoxSchema is the schema of a WGS84 bounding box, [minLon, minLat, maxLon,
// maxLat], as both the bbox output of plot-bbox and the input of bag-panden-fetch.
var BBoxSchema = map[string]any{
	"type":     "array",
	"items":    map[string]any{"type": "number"},
	"minItems": 4,
	"maxItems": 4,
}

// OverlapsSchema is the schema of the overlaps output of plot-bag-overlap and
// the overlaps input of bag-match-classify. overlap_ratio is unrounded here:
// bag-match-classify thresholds on it before rounding, which is what makes the
// chained steps agree with plot-bag-matching exactly.
var OverlapsSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type":     "object",
		"required": []string{"bag_id", "overlap_ratio"},
		"properties": map[string]any{
			"bag_id":           map[string]any{"type": "string"},
			"bouwjaar":         map[string]any{"type": []string{"integer", "null"}},
			"overlap_ratio":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"building_area_m2": map[string]any{"type": "number"},
			"overlap_area_m2":  map[string]any{"type": "number"},
		},
	},
}

// SkippedSchema is the schema of the skipped output of plot-bag-overlap.
var SkippedSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"missing_id_or_geometry":    map[string]any{"type": "integer"},
		"invalid_or_empty_geometry": map[string]any{"type": "integer"},
	},
}

// PlanDataIDSchema and PlanPlannaamSchema carry the plan identity through
// bag-match-classify, which no longer sees the plot object.
var PlanDataIDSchema = map[string]any{"type": "string"}

// PlanPlannaamSchema is the optional plan name echoed into the match result.
var PlanPlannaamSchema = map[string]any{"type": []string{"string", "null"}}
