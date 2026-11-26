// Package pdok gives the plotbag matching a BAG source of its own: instead of
// the caller supplying the BAG features, they are fetched from the public PDOK
// BAG WFS for the bounding box of the plot. The matching itself is unchanged —
// this package only does the I/O that plotbag deliberately leaves out.
//
// Only bag:pand is queried. The other BAG feature types PDOK serves
// (verblijfsobject, ligplaats, standplaats) have point geometry, and the
// overlap ratio plotbag computes is an area share, so points can never match.
package pdok

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ogc-api/processes/plotbag"
)

const (
	// DefaultWFSURL is the public PDOK BAG WFS. It needs no API key.
	DefaultWFSURL = "https://service.pdok.nl/lv/bag/wfs/v2_0"

	// Collection is the BAG feature type queried (see the package comment).
	Collection = "pand"

	// wfsCRS is the CRS asked of the WFS. In this urn form the axis order is
	// latitude, longitude — the reverse of GeoJSON — which applies to the bbox
	// parameter. The response itself comes back as CRS84 (lon, lat), which is
	// what plotbag expects.
	wfsCRS = "urn:ogc:def:crs:EPSG::4326"

	// pageSize is the WFS count per request; the service pages with startIndex.
	pageSize = 1000

	// maxFeatures caps one execution, so an oversized plot cannot exhaust memory.
	maxFeatures = 20000

	httpTimeout = 30 * time.Second

	// errBodyLimit is how much of a WFS error body (XML) is quoted back.
	errBodyLimit = 400
)

// WFSURL is the endpoint queried; main overrides it from PDOK_BAG_WFS_URL.
var WFSURL = DefaultWFSURL

// HTTPClient performs the WFS requests.
var HTTPClient = &http.Client{Timeout: httpTimeout}

// Logger is set from handlers.SetLogger, like plotbag.Logger.
var Logger = slog.Default()

// UpstreamError marks a failure of the PDOK service, as opposed to bad input.
// The handler reports it as 502 Bad Gateway.
type UpstreamError struct{ Msg string }

func (e *UpstreamError) Error() string { return e.Msg }

func upstreamErr(format string, args ...any) error {
	return &UpstreamError{Msg: fmt.Sprintf(format, args...)}
}

// QueryInfo is the bag_query output: what was asked of PDOK and what came back.
// It lets a caller tell "no BAG objects in the plot" apart from "nothing was
// fetched", which the match result alone does not show.
type QueryInfo struct {
	SourceURL    string     `json:"source_url"`
	Collection   string     `json:"collection"`
	BBox         [4]float64 `json:"bbox"`
	FeatureCount int        `json:"feature_count"`
	Requests     int        `json:"requests"`
	Truncated    bool       `json:"truncated"`
}

// Run is the process entry point for plot-bag-matching-pdok.
func Run(inputs map[string]any) (map[string]any, error) {
	plot, ok := inputs["plot"].(map[string]any)
	if !ok || plot["geometry"] == nil || plot["plan_dataid"] == nil {
		// Delegated so the message is identical to the by-value process.
		return plotbag.Run(inputs)
	}

	bbox, err := plotbag.BBoxOf(plot["geometry"])
	if err != nil {
		Logger.Warn("plot-bag-matching-pdok: plot geometry unusable",
			"plan_dataid", plot["plan_dataid"], "error", err.Error())
		return nil, &plotbag.InputError{
			Msg: fmt.Sprintf("plot.geometry could not be read as GeoJSON geometry: %v", err),
		}
	}

	features, info, err := FetchPanden(bbox)
	if err != nil {
		return nil, err
	}

	// plotbag.Run owns the remaining input validation (the ratios) and the
	// matching, so this process stays a thin BAG source in front of it.
	merged := make(map[string]any, len(inputs)+1)
	for k, v := range inputs {
		merged[k] = v
	}
	merged["bag_buildings"] = features

	out, err := plotbag.Run(merged)
	if err != nil {
		return nil, err
	}
	out["bag_query"] = info
	return out, nil
}

// FetchPanden returns the BAG panden whose geometry intersects bbox
// ([minLon, minLat, maxLon, maxLat], WGS84) as GeoJSON features in CRS84.
//
// Every building that overlaps the plot also overlaps the plot's bounding box,
// so the bbox query loses no candidate.
func FetchPanden(bbox [4]float64) ([]any, *QueryInfo, error) {
	info := &QueryInfo{SourceURL: WFSURL, Collection: Collection, BBox: bbox}
	features := []any{}

	for {
		page, err := fetchPage(bbox, len(features))
		if err != nil {
			Logger.Error("PDOK BAG WFS request failed",
				"url", WFSURL, "start_index", len(features), "error", err.Error())
			return nil, nil, err
		}
		info.Requests++
		features = append(features, page...)

		if len(page) < pageSize {
			break
		}
		if len(features) >= maxFeatures {
			info.Truncated = true
			Logger.Warn("PDOK BAG result truncated",
				"max_features", maxFeatures, "bbox", bbox)
			break
		}
	}

	info.FeatureCount = len(features)
	Logger.Info("PDOK BAG panden fetched",
		"count", len(features), "requests", info.Requests, "bbox", bbox)
	return features, info, nil
}

// fetchPage retrieves one page of features starting at startIndex.
func fetchPage(bbox [4]float64, startIndex int) ([]any, error) {
	q := url.Values{}
	q.Set("service", "WFS")
	q.Set("version", "2.0.0")
	q.Set("request", "GetFeature")
	q.Set("typeNames", "bag:"+Collection)
	q.Set("outputFormat", "application/json")
	q.Set("srsName", wfsCRS)
	// Latitude first: see wfsCRS.
	q.Set("bbox", fmt.Sprintf("%.8f,%.8f,%.8f,%.8f,%s", bbox[1], bbox[0], bbox[3], bbox[2], wfsCRS))
	q.Set("count", strconv.Itoa(pageSize))
	q.Set("startIndex", strconv.Itoa(startIndex))

	reqURL := WFSURL + "?" + q.Encode()
	resp, err := HTTPClient.Get(reqURL)
	if err != nil {
		return nil, upstreamErr("PDOK BAG WFS unreachable: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// WFS errors come back as an XML ExceptionReport; quote the start of it.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		return nil, upstreamErr("PDOK BAG WFS returned %d: %s", resp.StatusCode, collapse(string(body)))
	}

	var fc struct {
		Features []any `json:"features"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		return nil, upstreamErr("PDOK BAG WFS response is not GeoJSON: %v", err)
	}
	return fc.Features, nil
}

// collapse squeezes whitespace so an XML error fits on one log line.
func collapse(s string) string {
	out := make([]rune, 0, len(s))
	space := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !space && len(out) > 0 {
				out = append(out, ' ')
			}
			space = true
			continue
		}
		space = false
		out = append(out, r)
	}
	return string(out)
}

// ---------------------------------------------------------------- schemas --

// BAGQuerySchema is the schema of the bag_query output.
var BAGQuerySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"source_url":    map[string]any{"type": "string", "format": "uri"},
		"collection":    map[string]any{"type": "string"},
		"bbox":          map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
		"feature_count": map[string]any{"type": "integer"},
		"requests":      map[string]any{"type": "integer"},
		"truncated":     map[string]any{"type": "boolean"},
	},
}

// RunFetch is the entry point for bag-panden-fetch: the BAG fetch of
// plot-bag-matching-pdok as a step of its own, so a recipe can fetch once and
// reuse the features across several matching runs.
func RunFetch(inputs map[string]any) (map[string]any, error) {
	raw, ok := inputs["bbox"].([]any)
	if !ok {
		return nil, &plotbag.InputError{Msg: "input 'bbox' is required and must be an array of 4 numbers"}
	}
	if len(raw) != 4 {
		return nil, &plotbag.InputError{
			Msg: fmt.Sprintf("bbox must have exactly 4 numbers [minLon, minLat, maxLon, maxLat], got %d", len(raw)),
		}
	}
	var bbox [4]float64
	for i, v := range raw {
		f, ok := v.(float64)
		if !ok {
			return nil, &plotbag.InputError{Msg: fmt.Sprintf("bbox[%d] must be a number", i)}
		}
		bbox[i] = f
	}
	if bbox[0] > bbox[2] || bbox[1] > bbox[3] {
		return nil, &plotbag.InputError{Msg: "bbox must be ordered [minLon, minLat, maxLon, maxLat]"}
	}

	features, info, err := FetchPanden(bbox)
	if err != nil {
		return nil, err
	}
	return map[string]any{"bag_buildings": features, "bag_query": info}, nil
}
