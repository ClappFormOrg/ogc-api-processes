// Package plotbag matches a housing-plan plot (kavel) against BAG buildings
// (panden/verblijfsobjecten) and reports which BAG objects fall inside it.
//
// Ported from the "Process layer — Plot→BAG matching" notebook
// (test_Process_plot_bag_matching.ipynb). The package does no I/O: the caller
// supplies the BAG features for the search area, which keeps the matching
// logic platform-independent.
package plotbag

import (
	"fmt"
	"log/slog"
	"math"
)

const (
	// DefaultMinOverlapRatio is the minimum fraction of a building inside the
	// plot for it to count as a candidate match.
	DefaultMinOverlapRatio = 0.05
	// DefaultFullMatchThreshold is the overlap fraction from which a building
	// counts as fully matched.
	DefaultFullMatchThreshold = 0.95
)

// Flag reasons, kept identical to the notebook so downstream consumers don't change.
const (
	FlagNoBAGObject  = "geen_bag_object_binnen_kavel"
	FlagPartialOnly  = "alleen_partiele_match"
	MatchTypeFull    = "full"
	MatchTypePartial = "partial"
)

// Logger is used for warnings about skipped buildings and repaired geometry.
var Logger = slog.Default()

// InputError marks a problem with the caller's input (as opposed to an
// internal failure).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErr(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// Match is one BAG object overlapping the plot.
type Match struct {
	BagID        string  `json:"bag_id"`
	Bouwjaar     any     `json:"bouwjaar"`
	OverlapRatio float64 `json:"overlap_ratio"`
	Score        float64 `json:"score"`
	MatchType    string  `json:"match_type"`
}

// Summary aggregates the matches for a plot.
type Summary struct {
	NCandidates     int     `json:"n_candidates"`
	NFullMatches    int     `json:"n_full_matches"`
	NPartialMatches int     `json:"n_partial_matches"`
	FullyMatched    bool    `json:"fully_matched"`
	Flagged         bool    `json:"flagged"`
	FlagReason      *string `json:"flag_reason"`
}

// Result is the match_result output of the process.
type Result struct {
	PlanDataID   any     `json:"plan_dataid"`
	PlanPlannaam any     `json:"plan_plannaam"`
	Matches      []Match `json:"matches"`
	Summary      Summary `json:"summary"`
}

// MatchPlotToBAG matches one plot geometry against a list of BAG GeoJSON features.
//
// plot must contain "plan_dataid" and "geometry" (GeoJSON Polygon/MultiPolygon,
// WGS84). bagBuildings is a list of GeoJSON Features with
// properties.identificatie / .bouwjaar. Overlap is the share of a building's
// area that lies inside the plot, computed in RD New (EPSG:28992).
//
// Invalid input returns an *InputError. Self-intersecting plot geometry is
// repaired where possible. Individual buildings without an identifier or with
// unreadable geometry are skipped and counted in a warning log.
func MatchPlotToBAG(plot map[string]any, bagBuildings []any, minOverlapRatio, fullMatchThreshold float64) (*Result, error) {
	planID := plot["plan_dataid"]
	log := Logger.With("plan_dataid", planID)

	plotGeom, err := parseGeoJSON(plot["geometry"])
	if err != nil {
		log.Warn("plot geometry could not be read as GeoJSON", "error", err.Error())
		return nil, inputErr("plot.geometry could not be read as GeoJSON geometry: %v", err)
	}
	if plotGeom.isEmpty() {
		log.Warn("plot geometry is empty")
		return nil, inputErr("plot.geometry is empty")
	}
	if !plotGeom.isValid() {
		repaired := plotGeom.repair()
		if repaired.isEmpty() || !repaired.isValid() {
			log.Warn("invalid (self-intersecting) plot geometry could not be repaired")
			return nil, inputErr("plot.geometry is invalid (self-intersecting) and could not be repaired automatically")
		}
		log.Warn("invalid (self-intersecting) plot geometry repaired automatically")
		plotGeom = repaired
	}

	log.Info("matching started",
		"bag_buildings", len(bagBuildings),
		"min_overlap_ratio", minOverlapRatio,
		"full_match_threshold", fullMatchThreshold)

	overlaps, skipped := computeOverlaps(plotGeom, bagBuildings, minOverlapRatio)

	if skipped.MissingIDOrGeometry > 0 || skipped.InvalidGeometry > 0 {
		log.Warn("BAG buildings skipped",
			"missing_id_or_geometry", skipped.MissingIDOrGeometry,
			"invalid_or_empty_geometry", skipped.InvalidGeometry)
	}

	result := Classify(overlaps, planID, plot["plan_plannaam"], fullMatchThreshold)

	log.Info("matching finished",
		"candidates", result.Summary.NCandidates,
		"full", result.Summary.NFullMatches,
		"partial", result.Summary.NPartialMatches,
		"flagged", result.Summary.Flagged)

	return result, nil
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
