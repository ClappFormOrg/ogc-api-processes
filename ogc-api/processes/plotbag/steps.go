package plotbag

// The plot-bag matching as separately executable steps.
//
// MatchPlotToBAG runs the whole pipeline in one call. The same work is also
// reachable one stage at a time, so a recipe can chain the stages, inspect what
// each produced and swap one out. Both paths share the code below, so the
// chained steps and the single process cannot drift apart.
//
// Stage boundaries, and what crosses them:
//
//	plot            -> PrepareGeometry  -> geometry (WGS84), geometry_report
//	geometry        -> BBoxOf           -> bbox (WGS84)
//	bbox            -> pdok.FetchPanden -> bag_buildings, bag_query
//	geometry + bldg -> Overlaps         -> overlaps, skipped
//	overlaps        -> Classify         -> match_result
//
// One caveat worth knowing: PrepareGeometry validates and repairs in lon/lat,
// because its output has to be GeoJSON and rd.go transforms lon/lat to RD New
// and not back. MatchPlotToBAG repairs in RD New, as it always has. For valid
// geometry both paths agree exactly; for a self-intersecting polygon the
// repaired lobes can differ marginally, since the projection is not perfectly
// affine. Areas and overlaps are measured in RD New either way.

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ------------------------------------------------------------- step types --

// GeometryReport is the geometry_report output of plot-geometry-prepare: what
// the geometry was when it arrived and what had to be done to it.
type GeometryReport struct {
	Valid     bool       `json:"valid"`
	Repaired  bool       `json:"repaired"`
	NPolygons int        `json:"n_polygons"`
	NRings    int        `json:"n_rings"`
	AreaM2    float64    `json:"area_m2"`
	BBox      [4]float64 `json:"bbox"`
}

// Overlap is one BAG building that intersects the plot, before any
// classification. OverlapRatio is kept at full precision: Classify compares it
// against full_match_threshold and only then rounds, which is the order
// MatchPlotToBAG uses, so chaining the steps reproduces its output exactly.
type Overlap struct {
	BagID          string  `json:"bag_id"`
	Bouwjaar       any     `json:"bouwjaar"`
	OverlapRatio   float64 `json:"overlap_ratio"`
	BuildingAreaM2 float64 `json:"building_area_m2"`
	OverlapAreaM2  float64 `json:"overlap_area_m2"`
}

// SkipCounts reports BAG features that could not be used, which otherwise
// vanish silently: an empty overlaps list means something different when 400
// buildings were skipped than when none were.
type SkipCounts struct {
	MissingIDOrGeometry int `json:"missing_id_or_geometry"`
	InvalidGeometry     int `json:"invalid_or_empty_geometry"`
}

// ------------------------------------------------ step 1: prepare geometry --

// PrepareGeometry reads a plot geometry, repairs self-intersections where it
// can, and returns it as WGS84 GeoJSON together with a report. See the package
// note above on why this repairs in lon/lat rather than RD New.
func PrepareGeometry(geometry any) (map[string]any, *GeometryReport, error) {
	g, err := parseGeoJSONAs(geometry, false)
	if err != nil {
		return nil, nil, inputErr("geometry could not be read as GeoJSON geometry: %v", err)
	}
	if g.isEmpty() {
		return nil, nil, inputErr("geometry is empty")
	}

	rep := &GeometryReport{Valid: g.isValid()}
	if !rep.Valid {
		repaired := g.repair()
		if repaired.isEmpty() || !repaired.isValid() {
			Logger.Warn("invalid (self-intersecting) geometry could not be repaired")
			return nil, nil, inputErr("geometry is invalid (self-intersecting) and could not be repaired automatically")
		}
		Logger.Warn("invalid (self-intersecting) geometry repaired automatically")
		g = repaired
		rep.Repaired = true
	}

	out := g.toGeoJSON()

	// Area is only meaningful in metres, so measure the result in RD New.
	projected, err := parseGeoJSON(out)
	if err != nil {
		return nil, nil, inputErr("repaired geometry could not be re-read: %v", err)
	}
	bb, err := BBoxOf(out)
	if err != nil {
		return nil, nil, inputErr("%v", err)
	}
	rep.NPolygons = len(g)
	rep.NRings = len(g.rings())
	rep.AreaM2 = round2(projected.area())
	rep.BBox = bb
	return out, rep, nil
}

// ---------------------------------------------------- step 2: bounding box --

// BBoxOf returns [minLon, minLat, maxLon, maxLat] of a GeoJSON
// Polygon/MultiPolygon. Only the extent is read, so any nesting depth of
// positions is accepted.
func BBoxOf(g any) ([4]float64, error) {
	var bb [4]float64
	obj, ok := g.(map[string]any)
	if !ok {
		return bb, errors.New("geometry must be a GeoJSON object")
	}
	switch obj["type"] {
	case "Polygon", "MultiPolygon":
	default:
		return bb, fmt.Errorf("type %v is not Polygon or MultiPolygon", obj["type"])
	}
	coords, ok := obj["coordinates"].([]any)
	if !ok {
		return bb, errors.New("geometry has no coordinates array")
	}

	bb = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	n := 0

	var walk func(v any)
	walk = func(v any) {
		list, ok := v.([]any)
		if !ok {
			return
		}
		// A position is a flat [lon, lat, ...]; anything else nests further.
		if len(list) >= 2 {
			if lon, ok1 := list[0].(float64); ok1 {
				if lat, ok2 := list[1].(float64); ok2 {
					if !math.IsNaN(lon) && !math.IsNaN(lat) {
						bb[0] = math.Min(bb[0], lon)
						bb[1] = math.Min(bb[1], lat)
						bb[2] = math.Max(bb[2], lon)
						bb[3] = math.Max(bb[3], lat)
						n++
					}
					return
				}
			}
		}
		for _, item := range list {
			walk(item)
		}
	}
	walk(coords)

	if n == 0 {
		return bb, errors.New("geometry contains no usable positions")
	}
	return bb, nil
}

// --------------------------------------------------------- step 4: overlaps --

// Overlaps computes the area overlap of each BAG building with the plot and
// keeps those at or above minOverlapRatio. Thresholding into full and partial
// matches is left to Classify.
func Overlaps(geometry any, bagBuildings []any, minOverlapRatio float64) ([]Overlap, SkipCounts, error) {
	plotGeom, err := parseGeoJSON(geometry)
	if err != nil {
		return nil, SkipCounts{}, inputErr("geometry could not be read as GeoJSON geometry: %v", err)
	}
	if plotGeom.isEmpty() {
		return nil, SkipCounts{}, inputErr("geometry is empty")
	}
	overlaps, skipped := computeOverlaps(plotGeom, bagBuildings, minOverlapRatio)
	return overlaps, skipped, nil
}

// computeOverlaps is the per-building loop shared by Overlaps and
// MatchPlotToBAG. plotGeom must already be parsed in RD New.
func computeOverlaps(plotGeom multiPolygon, bagBuildings []any, minOverlapRatio float64) ([]Overlap, SkipCounts) {
	overlaps := []Overlap{}
	var skipped SkipCounts

	for _, f := range bagBuildings {
		feat, ok := f.(map[string]any)
		if !ok {
			skipped.MissingIDOrGeometry++
			continue
		}
		props, _ := feat["properties"].(map[string]any)
		bagID, _ := props["identificatie"].(string)
		geom := feat["geometry"]
		if bagID == "" || geom == nil {
			skipped.MissingIDOrGeometry++
			continue
		}
		building, err := parseGeoJSON(geom)
		if err != nil {
			skipped.InvalidGeometry++
			continue
		}
		buildingArea := building.area()
		if building.isEmpty() || buildingArea <= 0 {
			skipped.InvalidGeometry++
			continue
		}

		// A building that only touches the plot boundary has zero overlap area
		// and is never a candidate, even with min_overlap_ratio = 0.
		overlap := intersectionArea(plotGeom, building)
		if overlap <= 0 {
			continue
		}
		ratio := math.Min(overlap/buildingArea, 1)
		if ratio < minOverlapRatio {
			continue
		}
		overlaps = append(overlaps, Overlap{
			BagID:          bagID,
			Bouwjaar:       props["bouwjaar"],
			OverlapRatio:   ratio,
			BuildingAreaM2: round2(buildingArea),
			OverlapAreaM2:  round2(overlap),
		})
	}
	return overlaps, skipped
}

// --------------------------------------------------------- step 5: classify --

// Classify sorts the overlaps, splits them into full and partial matches at
// fullMatchThreshold and builds the match summary and flag.
func Classify(overlaps []Overlap, planID, planName any, fullMatchThreshold float64) *Result {
	matches := make([]Match, 0, len(overlaps))
	for _, o := range overlaps {
		matchType := MatchTypePartial
		if o.OverlapRatio >= fullMatchThreshold {
			matchType = MatchTypeFull
		}
		rounded := round4(o.OverlapRatio)
		matches = append(matches, Match{
			BagID:        o.BagID,
			Bouwjaar:     o.Bouwjaar,
			OverlapRatio: rounded,
			Score:        rounded,
			MatchType:    matchType,
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].OverlapRatio > matches[j].OverlapRatio
	})

	nFull := 0
	for _, m := range matches {
		if m.MatchType == MatchTypeFull {
			nFull++
		}
	}
	fullyMatched := nFull > 0

	var flagReason *string
	switch {
	case len(matches) == 0:
		r := FlagNoBAGObject
		flagReason = &r
	case !fullyMatched:
		r := FlagPartialOnly
		flagReason = &r
	}

	return &Result{
		PlanDataID:   planID,
		PlanPlannaam: planName,
		Matches:      matches,
		Summary: Summary{
			NCandidates:     len(matches),
			NFullMatches:    nFull,
			NPartialMatches: len(matches) - nFull,
			FullyMatched:    fullyMatched,
			Flagged:         !fullyMatched,
			FlagReason:      flagReason,
		},
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ------------------------------------------------------ process entry points --
// One Run function per step process. They do the input coercion that the
// handler layer does not, and return outputs keyed by output ID, exactly like
// Run does for the whole-pipeline process.

// RunPrepareGeometry is the entry point for plot-geometry-prepare. It takes
// "geometry" directly, or falls back to "plot".geometry so a recipe can hand it
// a whole plot record without unwrapping it first.
func RunPrepareGeometry(inputs map[string]any) (map[string]any, error) {
	geom := inputs["geometry"]
	if geom == nil {
		if plot, ok := inputs["plot"].(map[string]any); ok {
			geom = plot["geometry"]
		}
	}
	if geom == nil {
		return nil, inputErr("input 'geometry' is required (or a 'plot' containing one)")
	}
	out, report, err := PrepareGeometry(geom)
	if err != nil {
		return nil, err
	}
	return map[string]any{"geometry": out, "geometry_report": report}, nil
}

// RunBBox is the entry point for plot-bbox.
func RunBBox(inputs map[string]any) (map[string]any, error) {
	geom, ok := inputs["geometry"].(map[string]any)
	if !ok {
		return nil, inputErr("input 'geometry' is required and must be a GeoJSON geometry object")
	}
	bb, err := BBoxOf(geom)
	if err != nil {
		return nil, inputErr("%v", err)
	}
	return map[string]any{"bbox": bb}, nil
}

// RunOverlaps is the entry point for plot-bag-overlap.
func RunOverlaps(inputs map[string]any) (map[string]any, error) {
	geom, ok := inputs["geometry"].(map[string]any)
	if !ok {
		return nil, inputErr("input 'geometry' is required and must be a GeoJSON geometry object")
	}
	var buildings []any
	if v, present := inputs["bag_buildings"]; present && v != nil {
		list, ok := v.([]any)
		if !ok {
			return nil, inputErr("bag_buildings must be a list")
		}
		buildings = list
	}
	minOverlap, err := ratioInput(inputs, "min_overlap_ratio", DefaultMinOverlapRatio)
	if err != nil {
		return nil, err
	}
	overlaps, skipped, err := Overlaps(geom, buildings, minOverlap)
	if err != nil {
		return nil, err
	}
	return map[string]any{"overlaps": overlaps, "skipped": skipped}, nil
}

// RunClassify is the entry point for bag-match-classify. It reads the overlaps
// back from JSON, so it works whether they came straight from plot-bag-overlap
// or were edited in between.
func RunClassify(inputs map[string]any) (map[string]any, error) {
	planID := inputs["plan_dataid"]
	if planID == nil {
		return nil, inputErr("input 'plan_dataid' is required")
	}
	raw, present := inputs["overlaps"]
	if !present || raw == nil {
		return nil, inputErr("input 'overlaps' is required")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, inputErr("overlaps must be a list")
	}
	overlaps := make([]Overlap, 0, len(list))
	for i, item := range list {
		o, ok := item.(map[string]any)
		if !ok {
			return nil, inputErr("overlaps[%d] must be an object", i)
		}
		bagID, _ := o["bag_id"].(string)
		if bagID == "" {
			return nil, inputErr("overlaps[%d].bag_id is required", i)
		}
		ratio, ok := o["overlap_ratio"].(float64)
		if !ok {
			return nil, inputErr("overlaps[%d].overlap_ratio must be a number", i)
		}
		if ratio < 0 || ratio > 1 {
			return nil, inputErr("overlaps[%d].overlap_ratio must be between 0 and 1", i)
		}
		overlaps = append(overlaps, Overlap{BagID: bagID, Bouwjaar: o["bouwjaar"], OverlapRatio: ratio})
	}
	fullThreshold, err := ratioInput(inputs, "full_match_threshold", DefaultFullMatchThreshold)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"match_result": Classify(overlaps, planID, inputs["plan_plannaam"], fullThreshold),
	}, nil
}
