package plotbag

import (
	"errors"
	"math"
	"testing"
)

// square returns a closed GeoJSON-style ring for an axis-aligned lon/lat box.
func square(x0, y0, x1, y1 float64) []any {
	return []any{
		[]any{x0, y0}, []any{x1, y0}, []any{x1, y1}, []any{x0, y1}, []any{x0, y0},
	}
}

func polygonGeom(rings ...[]any) map[string]any {
	coords := make([]any, len(rings))
	for i, r := range rings {
		coords[i] = r
	}
	return map[string]any{"type": "Polygon", "coordinates": coords}
}

func feature(id string, bouwjaar any, geom map[string]any) map[string]any {
	return map[string]any{
		"type":       "Feature",
		"properties": map[string]any{"identificatie": id, "bouwjaar": bouwjaar},
		"geometry":   geom,
	}
}

// The notebook's synthetic self-test: one building fully inside, one half over
// the boundary, one fully outside.
func testPlot() map[string]any {
	return map[string]any{
		"plan_dataid":   "test-plan-1",
		"plan_plannaam": "Testkavel",
		"geometry":      polygonGeom(square(5.000, 52.000, 5.002, 52.002)),
	}
}

func testBuildings() []any {
	return []any{
		feature("NL.IMBAG.PAND.0001", 2019.0, polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))),
		feature("NL.IMBAG.PAND.0002", 2020.0, polygonGeom(square(5.0015, 52.0010, 5.0025, 52.0013))),
		feature("NL.IMBAG.PAND.0003", 1998.0, polygonGeom(square(5.010, 52.010, 5.011, 52.011))),
	}
}

func TestToRD_ReferencePoint(t *testing.T) {
	x, y := toRD(rdLam0, rdPhi0)
	if x != rdX0 || y != rdY0 {
		t.Fatalf("reference point should map to (%v, %v), got (%v, %v)", rdX0, rdY0, x, y)
	}
}

func TestToRD_KnownLocation(t *testing.T) {
	// Dam, Amsterdam: ~ (121400, 487400) in RD New.
	x, y := toRD(4.8932, 52.3731)
	if math.Abs(x-121400) > 200 || math.Abs(y-487400) > 200 {
		t.Errorf("Dam square projected to (%.0f, %.0f), expected ≈ (121400, 487400)", x, y)
	}
}

func TestMatch_SyntheticPlot(t *testing.T) {
	res, err := MatchPlotToBAG(testPlot(), testBuildings(), DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(res.Matches), res.Matches)
	}
	full, partial := res.Matches[0], res.Matches[1]
	if full.BagID != "NL.IMBAG.PAND.0001" || full.MatchType != MatchTypeFull || full.OverlapRatio != 1 {
		t.Errorf("unexpected full match: %+v", full)
	}
	if partial.BagID != "NL.IMBAG.PAND.0002" || partial.MatchType != MatchTypePartial || partial.OverlapRatio != 0.5 {
		t.Errorf("unexpected partial match: %+v", partial)
	}
	s := res.Summary
	if s.NCandidates != 2 || s.NFullMatches != 1 || s.NPartialMatches != 1 || !s.FullyMatched || s.Flagged || s.FlagReason != nil {
		t.Errorf("unexpected summary: %+v", s)
	}
}

func TestMatch_NoBuildingsFlagged(t *testing.T) {
	res, err := MatchPlotToBAG(testPlot(), nil, DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Summary.Flagged || res.Summary.FlagReason == nil || *res.Summary.FlagReason != FlagNoBAGObject {
		t.Errorf("expected flag %q, got %+v", FlagNoBAGObject, res.Summary)
	}
	if res.Matches == nil {
		t.Error("matches should be an empty list, not null")
	}
}

func TestMatch_PartialOnlyFlagged(t *testing.T) {
	res, err := MatchPlotToBAG(testPlot(), testBuildings()[1:2], DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Summary.FlagReason == nil || *res.Summary.FlagReason != FlagPartialOnly {
		t.Errorf("expected flag %q, got %+v", FlagPartialOnly, res.Summary)
	}
}

func TestMatch_PlotWithHole(t *testing.T) {
	// The fully-inside building sits in a hole, so it no longer matches.
	plot := testPlot()
	plot["geometry"] = polygonGeom(
		square(5.000, 52.000, 5.002, 52.002),
		square(5.0004, 52.0004, 5.0009, 52.0009),
	)
	res, err := MatchPlotToBAG(plot, testBuildings(), DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Matches) != 1 || res.Matches[0].BagID != "NL.IMBAG.PAND.0002" {
		t.Errorf("expected only the partial building, got %+v", res.Matches)
	}
}

func TestMatch_NonConvexPlot(t *testing.T) {
	// L-shaped plot; the building sits in the notch and must not match.
	plot := testPlot()
	plot["geometry"] = polygonGeom([]any{
		[]any{5.000, 52.000}, []any{5.002, 52.000}, []any{5.002, 52.001},
		[]any{5.001, 52.001}, []any{5.001, 52.002}, []any{5.000, 52.002}, []any{5.000, 52.000},
	})
	notch := feature("NOTCH", nil, polygonGeom(square(5.0013, 52.0013, 5.0017, 52.0017)))
	res, err := MatchPlotToBAG(plot, []any{notch}, DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Matches) != 0 {
		t.Errorf("building in the notch should not match, got %+v", res.Matches)
	}
}

func TestMatch_BowtieRepaired(t *testing.T) {
	plot := map[string]any{
		"plan_dataid": "test-plan-bowtie",
		"geometry": polygonGeom([]any{
			[]any{5.000, 52.000}, []any{5.002, 52.002}, []any{5.002, 52.000},
			[]any{5.000, 52.002}, []any{5.000, 52.000},
		}),
	}
	// A building inside the right-hand lobe must be found after repair.
	lobe := feature("LOBE", nil, polygonGeom(square(5.0017, 52.0009, 5.0019, 52.0011)))
	res, err := MatchPlotToBAG(plot, []any{lobe}, DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("bowtie should be repaired, got error: %v", err)
	}
	if len(res.Matches) != 1 || res.Matches[0].MatchType != MatchTypeFull {
		t.Errorf("expected one full match in the repaired lobe, got %+v", res.Matches)
	}
}

func TestMatch_SkipsIncompleteBuildings(t *testing.T) {
	buildings := []any{
		"not a feature",
		map[string]any{"type": "Feature", "properties": map[string]any{}, "geometry": polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))},
		feature("NO-GEOM", nil, nil),
		feature("POINT", nil, map[string]any{"type": "Point", "coordinates": []any{5.001, 52.001}}),
		testBuildings()[0],
	}
	res, err := MatchPlotToBAG(testPlot(), buildings, DefaultMinOverlapRatio, DefaultFullMatchThreshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Matches) != 1 {
		t.Errorf("expected only the valid building to match, got %+v", res.Matches)
	}
}

func TestMatch_InvalidPlotGeometry(t *testing.T) {
	cases := map[string]any{
		"not an object":    "POLYGON((0 0))",
		"point":            map[string]any{"type": "Point", "coordinates": []any{5.0, 52.0}},
		"too few vertices": polygonGeom([]any{[]any{5.0, 52.0}, []any{5.1, 52.0}, []any{5.0, 52.0}}),
		"empty":            map[string]any{"type": "Polygon", "coordinates": []any{}},
		"non-numeric":      polygonGeom([]any{[]any{"a", "b"}, []any{5.1, 52.0}, []any{5.1, 52.1}, []any{5.0, 52.0}}),
	}
	for name, geom := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := MatchPlotToBAG(map[string]any{"plan_dataid": "x", "geometry": geom}, nil, 0.05, 0.95)
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("expected InputError, got %v", err)
			}
		})
	}
}

func TestRun_InputValidation(t *testing.T) {
	cases := map[string]map[string]any{
		"bag_buildings not a list":     {"plot": testPlot(), "bag_buildings": map[string]any{"not": "a list"}},
		"min_overlap_ratio not number": {"plot": testPlot(), "min_overlap_ratio": "abc"},
		"min_overlap_ratio > 1":        {"plot": testPlot(), "min_overlap_ratio": 2.0},
		"plot missing":                 {"bag_buildings": []any{}},
		"plot not an object":           {"plot": "not-a-dict"},
		"plot without geometry":        {"plot": map[string]any{"plan_dataid": "x"}},
	}
	for name, inputs := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Run(inputs)
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("expected InputError, got %v", err)
			}
		})
	}
}

func TestRun_Outputs(t *testing.T) {
	out, err := Run(map[string]any{"plot": testPlot(), "bag_buildings": testBuildings()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, ok := out["match_result"].(*Result)
	if !ok {
		t.Fatalf("expected match_result output, got %#v", out)
	}
	if res.PlanDataID != "test-plan-1" || res.Summary.NCandidates != 2 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestIntersectionArea_Symmetric(t *testing.T) {
	a, _ := parseGeoJSON(polygonGeom(square(5.000, 52.000, 5.002, 52.002)))
	b, _ := parseGeoJSON(polygonGeom(square(5.001, 52.001, 5.003, 52.003)))
	ab, ba := intersectionArea(a, b), intersectionArea(b, a)
	if math.Abs(ab-ba) > 1e-9*ab {
		t.Errorf("intersection area not symmetric: %v vs %v", ab, ba)
	}
	if ratio := ab / a.area(); math.Abs(ratio-0.25) > 0.001 {
		t.Errorf("expected quarter overlap, got %v", ratio)
	}
}
