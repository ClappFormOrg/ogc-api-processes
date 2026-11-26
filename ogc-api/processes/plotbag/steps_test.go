package plotbag

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

// roundTrip sends a value through JSON and back, the way it travels between two
// step processes over HTTP. Without this the tests would compare Go structs
// that never crossed a process boundary, which is not what a recipe does.
func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// chain runs the steps the way a recipe would: every hand-off goes through
// JSON, and each step is entered through its process entry point.
func chain(t *testing.T, plot map[string]any, buildings []any, minOverlap, fullThreshold float64) map[string]any {
	t.Helper()

	prep, err := RunPrepareGeometry(map[string]any{"geometry": plot["geometry"]})
	if err != nil {
		t.Fatalf("step 1 plot-geometry-prepare: %v", err)
	}
	geometry := roundTrip(t, prep["geometry"])

	// Step 2 is exercised for its own sake; the overlap step takes the
	// geometry, not the bbox, so its output only has to be well formed.
	bboxOut, err := RunBBox(map[string]any{"geometry": geometry})
	if err != nil {
		t.Fatalf("step 2 plot-bbox: %v", err)
	}
	if bb, ok := roundTrip(t, bboxOut["bbox"]).([]any); !ok || len(bb) != 4 {
		t.Fatalf("step 2 should return 4 numbers, got %v", bboxOut["bbox"])
	}

	// Step 3 (bag-panden-fetch) is the only step that does I/O; here the
	// caller supplies the same features it would have returned.
	ovOut, err := RunOverlaps(map[string]any{
		"geometry":          geometry,
		"bag_buildings":     buildings,
		"min_overlap_ratio": minOverlap,
	})
	if err != nil {
		t.Fatalf("step 4 plot-bag-overlap: %v", err)
	}

	clsOut, err := RunClassify(map[string]any{
		"overlaps":             roundTrip(t, ovOut["overlaps"]),
		"plan_dataid":          plot["plan_dataid"],
		"plan_plannaam":        plot["plan_plannaam"],
		"full_match_threshold": fullThreshold,
	})
	if err != nil {
		t.Fatalf("step 5 bag-match-classify: %v", err)
	}
	return clsOut
}

// TestSteps_ChainMatchesMonolith is the guarantee the split rests on: running
// the stages one at a time, passing each output through JSON, must produce the
// same match_result as the single plot-bag-matching call.
func TestSteps_ChainMatchesMonolith(t *testing.T) {
	cases := []struct {
		name          string
		plot          map[string]any
		buildings     []any
		minOverlap    float64
		fullThreshold float64
	}{
		{"synthetic plot", testPlot(), testBuildings(), DefaultMinOverlapRatio, DefaultFullMatchThreshold},
		{"no buildings", testPlot(), nil, DefaultMinOverlapRatio, DefaultFullMatchThreshold},
		{"threshold excludes all", testPlot(), testBuildings(), 0.99, DefaultFullMatchThreshold},
		{"zero min overlap", testPlot(), testBuildings(), 0, DefaultFullMatchThreshold},
		{"every match is full", testPlot(), testBuildings(), DefaultMinOverlapRatio, 0},
		{"incomplete buildings", testPlot(), []any{
			feature("", 2019.0, polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))),
			feature("NL.IMBAG.PAND.0001", 2019.0, nil),
			feature("NL.IMBAG.PAND.0002", 2020.0, polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))),
		}, DefaultMinOverlapRatio, DefaultFullMatchThreshold},
		{"plot with hole", map[string]any{
			"plan_dataid":   "test-plan-hole",
			"plan_plannaam": "Kavel met gat",
			"geometry": polygonGeom(
				square(5.000, 52.000, 5.002, 52.002),
				square(5.0008, 52.0008, 5.0012, 52.0012),
			),
		}, testBuildings(), DefaultMinOverlapRatio, DefaultFullMatchThreshold},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := MatchPlotToBAG(tc.plot, tc.buildings, tc.minOverlap, tc.fullThreshold)
			if err != nil {
				t.Fatalf("MatchPlotToBAG: %v", err)
			}
			got := chain(t, tc.plot, tc.buildings, tc.minOverlap, tc.fullThreshold)

			// Compare as JSON: that is what a client of either route sees.
			if !reflect.DeepEqual(roundTrip(t, got["match_result"]), roundTrip(t, want)) {
				gotJSON, _ := json.MarshalIndent(got["match_result"], "", "  ")
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("chained steps and plot-bag-matching disagree\nchain:\n%s\nmonolith:\n%s", gotJSON, wantJSON)
			}
		})
	}
}

// TestSteps_ReclassifyWithoutRecomputing is the point of splitting 4 from 5: a
// recipe can sweep full_match_threshold without recomputing any geometry.
func TestSteps_ReclassifyWithoutRecomputing(t *testing.T) {
	plot := testPlot()
	ovOut, err := RunOverlaps(map[string]any{
		"geometry":          plot["geometry"],
		"bag_buildings":     testBuildings(),
		"min_overlap_ratio": 0.0,
	})
	if err != nil {
		t.Fatalf("plot-bag-overlap: %v", err)
	}
	overlaps := roundTrip(t, ovOut["overlaps"])

	for _, threshold := range []float64{0, 0.5, 0.95, 1} {
		clsOut, err := RunClassify(map[string]any{
			"overlaps":             overlaps,
			"plan_dataid":          plot["plan_dataid"],
			"full_match_threshold": threshold,
		})
		if err != nil {
			t.Fatalf("bag-match-classify at %v: %v", threshold, err)
		}
		want, err := MatchPlotToBAG(plot, testBuildings(), 0.0, threshold)
		if err != nil {
			t.Fatalf("MatchPlotToBAG at %v: %v", threshold, err)
		}
		got := clsOut["match_result"].(*Result)
		if got.Summary.NFullMatches != want.Summary.NFullMatches {
			t.Errorf("threshold %v: n_full_matches = %d, want %d",
				threshold, got.Summary.NFullMatches, want.Summary.NFullMatches)
		}
	}
}

func TestSteps_PrepareGeometryReport(t *testing.T) {
	// A 0.002 x 0.002 degree box near 52N is approximately 137 x 222 m.
	out, err := RunPrepareGeometry(map[string]any{
		"geometry": polygonGeom(square(5.000, 52.000, 5.002, 52.002)),
	})
	if err != nil {
		t.Fatalf("plot-geometry-prepare: %v", err)
	}
	rep := out["geometry_report"].(*GeometryReport)
	if !rep.Valid || rep.Repaired {
		t.Errorf("a plain square should be valid and unrepaired, got valid=%v repaired=%v", rep.Valid, rep.Repaired)
	}
	if rep.NPolygons != 1 || rep.NRings != 1 {
		t.Errorf("n_polygons/n_rings = %d/%d, want 1/1", rep.NPolygons, rep.NRings)
	}
	if rep.AreaM2 < 25000 || rep.AreaM2 > 35000 {
		t.Errorf("area_m2 = %v, want roughly 30000 for a 137x222 m box", rep.AreaM2)
	}
	if rep.BBox != [4]float64{5.000, 52.000, 5.002, 52.002} {
		t.Errorf("bbox = %v, want the input extent", rep.BBox)
	}

	// The emitted geometry must be WGS84 GeoJSON, not RD metres: a recipe
	// feeds it straight into the next step.
	geom := out["geometry"].(map[string]any)
	coords := geom["coordinates"].([]any)[0].([]any)
	first := coords[0].([]any)
	if lon := first[0].(float64); lon < 3 || lon > 8 {
		t.Errorf("emitted geometry should be WGS84 lon/lat, got first position %v", first)
	}
	// Rings come back closed, as GeoJSON requires.
	last := coords[len(coords)-1].([]any)
	if first[0] != last[0] || first[1] != last[1] {
		t.Errorf("emitted ring should be closed, got first=%v last=%v", first, last)
	}
}

func TestSteps_PrepareGeometryRepairsBowtie(t *testing.T) {
	bowtie := polygonGeom([]any{
		[]any{5.000, 52.000}, []any{5.002, 52.002}, []any{5.002, 52.000}, []any{5.000, 52.002}, []any{5.000, 52.000},
	})
	out, err := RunPrepareGeometry(map[string]any{"geometry": bowtie})
	if err != nil {
		t.Fatalf("a bowtie should be repaired, got: %v", err)
	}
	rep := out["geometry_report"].(*GeometryReport)
	if rep.Valid {
		t.Error("a self-intersecting bowtie should be reported as invalid on input")
	}
	if !rep.Repaired {
		t.Error("a bowtie should be reported as repaired")
	}
	if rep.NPolygons != 2 {
		t.Errorf("a repaired bowtie should have 2 lobes, got %d", rep.NPolygons)
	}
}

func TestSteps_BBoxOf(t *testing.T) {
	bb, err := BBoxOf(polygonGeom(square(5.0, 52.0, 5.002, 52.002)))
	if err != nil {
		t.Fatalf("BBoxOf: %v", err)
	}
	if bb != [4]float64{5.0, 52.0, 5.002, 52.002} {
		t.Errorf("bbox = %v, want [5 52 5.002 52.002]", bb)
	}

	for _, bad := range []any{
		nil,
		"not an object",
		map[string]any{"type": "Point", "coordinates": []any{5.0, 52.0}},
		map[string]any{"type": "Polygon"},
		map[string]any{"type": "Polygon", "coordinates": []any{}},
	} {
		if _, err := BBoxOf(bad); err == nil {
			t.Errorf("BBoxOf(%v) should fail", bad)
		}
	}
}

func TestSteps_InputValidation(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"prepare without geometry", func() error {
			_, err := RunPrepareGeometry(map[string]any{})
			return err
		}},
		{"prepare with empty geometry", func() error {
			_, err := RunPrepareGeometry(map[string]any{
				"geometry": map[string]any{"type": "Polygon", "coordinates": []any{}},
			})
			return err
		}},
		{"bbox without geometry", func() error {
			_, err := RunBBox(map[string]any{})
			return err
		}},
		{"overlap without geometry", func() error {
			_, err := RunOverlaps(map[string]any{"bag_buildings": testBuildings()})
			return err
		}},
		{"overlap with non-list buildings", func() error {
			_, err := RunOverlaps(map[string]any{
				"geometry":      testPlot()["geometry"],
				"bag_buildings": "not a list",
			})
			return err
		}},
		{"overlap with out-of-range ratio", func() error {
			_, err := RunOverlaps(map[string]any{
				"geometry":          testPlot()["geometry"],
				"min_overlap_ratio": 1.5,
			})
			return err
		}},
		{"classify without plan_dataid", func() error {
			_, err := RunClassify(map[string]any{"overlaps": []any{}})
			return err
		}},
		{"classify without overlaps", func() error {
			_, err := RunClassify(map[string]any{"plan_dataid": "x"})
			return err
		}},
		{"classify with non-list overlaps", func() error {
			_, err := RunClassify(map[string]any{"plan_dataid": "x", "overlaps": "nope"})
			return err
		}},
		{"classify with overlap missing bag_id", func() error {
			_, err := RunClassify(map[string]any{
				"plan_dataid": "x",
				"overlaps":    []any{map[string]any{"overlap_ratio": 0.5}},
			})
			return err
		}},
		{"classify with non-numeric ratio", func() error {
			_, err := RunClassify(map[string]any{
				"plan_dataid": "x",
				"overlaps":    []any{map[string]any{"bag_id": "a", "overlap_ratio": "high"}},
			})
			return err
		}},
		{"classify with ratio above 1", func() error {
			_, err := RunClassify(map[string]any{
				"plan_dataid": "x",
				"overlaps":    []any{map[string]any{"bag_id": "a", "overlap_ratio": 1.5}},
			})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected an error")
			}
			var inputError *InputError
			if !errors.As(err, &inputError) {
				t.Fatalf("expected an *InputError so the handler answers 400, got %T: %v", err, err)
			}
		})
	}
}

// TestSteps_ClassifyEmptyOverlaps covers the flag a recipe is most likely to
// branch on: nothing matched at all.
func TestSteps_ClassifyEmptyOverlaps(t *testing.T) {
	out, err := RunClassify(map[string]any{"plan_dataid": "p1", "overlaps": []any{}})
	if err != nil {
		t.Fatalf("bag-match-classify: %v", err)
	}
	res := out["match_result"].(*Result)
	if res.Summary.FlagReason == nil || *res.Summary.FlagReason != FlagNoBAGObject {
		t.Errorf("flag_reason = %v, want %q", res.Summary.FlagReason, FlagNoBAGObject)
	}
	if !res.Summary.Flagged || res.Summary.FullyMatched {
		t.Error("an empty overlap list should be flagged and not fully matched")
	}
}

// TestSteps_OverlapAreasAreConsistent checks the areas reported alongside each
// ratio, which a recipe may use to sanity-check a match.
func TestSteps_OverlapAreasAreConsistent(t *testing.T) {
	out, err := RunOverlaps(map[string]any{
		"geometry":          testPlot()["geometry"],
		"bag_buildings":     testBuildings(),
		"min_overlap_ratio": 0.0,
	})
	if err != nil {
		t.Fatalf("plot-bag-overlap: %v", err)
	}
	overlaps := out["overlaps"].([]Overlap)
	if len(overlaps) == 0 {
		t.Fatal("expected at least one overlap")
	}
	for _, o := range overlaps {
		if o.BuildingAreaM2 <= 0 {
			t.Errorf("%s: building_area_m2 = %v, want > 0", o.BagID, o.BuildingAreaM2)
		}
		if o.OverlapAreaM2 > o.BuildingAreaM2+1 {
			t.Errorf("%s: overlap_area_m2 %v exceeds building_area_m2 %v", o.BagID, o.OverlapAreaM2, o.BuildingAreaM2)
		}
		if want := o.OverlapAreaM2 / o.BuildingAreaM2; math.Abs(want-o.OverlapRatio) > 0.01 {
			t.Errorf("%s: overlap_ratio %v does not match the reported areas (%v)", o.BagID, o.OverlapRatio, want)
		}
	}
}
