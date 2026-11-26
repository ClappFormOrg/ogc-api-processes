package pdok

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"ogc-api/processes/plotbag"
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

func pand(id string, bouwjaar any, geom map[string]any) map[string]any {
	return map[string]any{
		"type":       "Feature",
		"properties": map[string]any{"identificatie": id, "bouwjaar": bouwjaar},
		"geometry":   geom,
	}
}

func testPlot() map[string]any {
	return map[string]any{
		"plan_dataid":   "test-plan-1",
		"plan_plannaam": "Testkavel",
		"geometry":      polygonGeom(square(5.000, 52.000, 5.002, 52.002)),
	}
}

// fakeWFS serves pages of features and records the requests it received.
// It mimics the PDOK WFS: GeoJSON out, paging through count/startIndex.
func fakeWFS(t *testing.T, features []any) (*httptest.Server, *[]string) {
	t.Helper()
	var urls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urls = append(urls, r.URL.String())
		start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if count <= 0 {
			count = len(features)
		}
		end := start + count
		if end > len(features) {
			end = len(features)
		}
		page := []any{}
		if start < len(features) {
			page = features[start:end]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "FeatureCollection", "features": page,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &urls
}

// useWFS points the package at srv for the duration of the test.
func useWFS(t *testing.T, url string) {
	t.Helper()
	prev := WFSURL
	WFSURL = url
	t.Cleanup(func() { WFSURL = prev })
}

func TestGeometryBBox(t *testing.T) {
	tests := []struct {
		name string
		geom any
		want [4]float64
	}{
		{
			name: "polygon",
			geom: polygonGeom(square(5.000, 52.000, 5.002, 52.002)),
			want: [4]float64{5.000, 52.000, 5.002, 52.002},
		},
		{
			name: "polygon with hole does not widen the extent",
			geom: polygonGeom(square(5.000, 52.000, 5.002, 52.002), square(5.0005, 52.0005, 5.001, 52.001)),
			want: [4]float64{5.000, 52.000, 5.002, 52.002},
		},
		{
			name: "multipolygon spans both parts",
			geom: map[string]any{
				"type": "MultiPolygon",
				"coordinates": []any{
					[]any{square(5.000, 52.000, 5.001, 52.001)},
					[]any{square(5.010, 52.010, 5.012, 52.013)},
				},
			},
			want: [4]float64{5.000, 52.000, 5.012, 52.013},
		},
		{
			name: "positions with elevation",
			geom: map[string]any{
				"type": "Polygon",
				"coordinates": []any{[]any{
					[]any{5.0, 52.0, 3.0}, []any{5.1, 52.0, 3.0}, []any{5.1, 52.1, 3.0}, []any{5.0, 52.0, 3.0},
				}},
			},
			want: [4]float64{5.0, 52.0, 5.1, 52.1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plotbag.BBoxOf(tt.geom)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for i := range got {
				if math.Abs(got[i]-tt.want[i]) > 1e-9 {
					t.Fatalf("bbox = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestGeometryBBox_Invalid(t *testing.T) {
	tests := []struct {
		name string
		geom any
	}{
		{"not an object", "Polygon"},
		{"point geometry", map[string]any{"type": "Point", "coordinates": []any{5.0, 52.0}}},
		{"no coordinates", map[string]any{"type": "Polygon"}},
		{"empty coordinates", map[string]any{"type": "Polygon", "coordinates": []any{}}},
		{"non-numeric positions", map[string]any{
			"type": "Polygon", "coordinates": []any{[]any{[]any{"a", "b"}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := plotbag.BBoxOf(tt.geom); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestFetchPanden_BBoxIsLatitudeFirst(t *testing.T) {
	srv, urls := fakeWFS(t, []any{})
	useWFS(t, srv.URL)

	if _, _, err := FetchPanden([4]float64{5.000, 52.000, 5.002, 52.002}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*urls) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*urls))
	}
	u, err := url.Parse((*urls)[0])
	if err != nil {
		t.Fatalf("unparsable request URL: %v", err)
	}
	q := u.Query()
	// The urn form of EPSG:4326 is latitude first, so the plot's southern
	// latitude must come before its western longitude.
	want := "52.00000000,5.00000000,52.00200000,5.00200000," + wfsCRS
	if got := q.Get("bbox"); got != want {
		t.Fatalf("bbox = %q, want %q", got, want)
	}
	for param, value := range map[string]string{
		"typeNames":    "bag:pand",
		"outputFormat": "application/json",
		"request":      "GetFeature",
		"service":      "WFS",
		"version":      "2.0.0",
		"srsName":      wfsCRS,
	} {
		if got := q.Get(param); got != value {
			t.Errorf("%s = %q, want %q", param, got, value)
		}
	}
}

func TestFetchPanden_Pages(t *testing.T) {
	// One more than a full page, so a second request is needed.
	features := make([]any, pageSize+7)
	for i := range features {
		features[i] = pand(fmt.Sprintf("NL.IMBAG.PAND.%04d", i), 2000.0,
			polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008)))
	}
	srv, urls := fakeWFS(t, features)
	useWFS(t, srv.URL)

	got, info, err := FetchPanden([4]float64{5.0, 52.0, 5.002, 52.002})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(features) {
		t.Fatalf("got %d features, want %d", len(got), len(features))
	}
	if info.Requests != 2 || len(*urls) != 2 {
		t.Fatalf("expected 2 requests, got info.Requests=%d urls=%d", info.Requests, len(*urls))
	}
	if !strings.Contains((*urls)[1], "startIndex="+strconv.Itoa(pageSize)) {
		t.Fatalf("second request should continue at startIndex=%d: %s", pageSize, (*urls)[1])
	}
	if info.Truncated {
		t.Error("result should not be marked truncated")
	}
	if info.FeatureCount != len(features) {
		t.Errorf("FeatureCount = %d, want %d", info.FeatureCount, len(features))
	}
}

func TestFetchPanden_UpstreamErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "http error carries the status and body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("<ows:ExceptionReport>\n  <ows:Exception\n    code=\"x\"/>\n</ows:ExceptionReport>"))
			},
			want: "returned 400",
		},
		{
			name: "non-JSON body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("<html>not geojson</html>"))
			},
			want: "not GeoJSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			useWFS(t, srv.URL)

			_, _, err := FetchPanden([4]float64{5.0, 52.0, 5.002, 52.002})
			if err == nil {
				t.Fatal("expected an error")
			}
			var ue *UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("error %v is not an *UpstreamError", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tt.want)
			}
			if strings.ContainsAny(err.Error(), "\n\r") {
				t.Errorf("error should be collapsed to one line: %q", err.Error())
			}
		})
	}
}

func TestFetchPanden_Unreachable(t *testing.T) {
	srv, _ := fakeWFS(t, []any{})
	url := srv.URL
	srv.Close()
	useWFS(t, url)

	_, _, err := FetchPanden([4]float64{5.0, 52.0, 5.002, 52.002})
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %v is not an *UpstreamError", err)
	}
}

func TestRun_MatchesFetchedPanden(t *testing.T) {
	// Inside the plot, straddling its edge, and well outside it.
	features := []any{
		pand("NL.IMBAG.PAND.0001", 2019.0, polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))),
		pand("NL.IMBAG.PAND.0002", 2020.0, polygonGeom(square(5.0015, 52.0010, 5.0025, 52.0013))),
		pand("NL.IMBAG.PAND.0003", 1998.0, polygonGeom(square(5.010, 52.010, 5.011, 52.011))),
	}
	srv, _ := fakeWFS(t, features)
	useWFS(t, srv.URL)

	out, err := Run(map[string]any{"plot": testPlot()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, ok := out["match_result"].(*plotbag.Result)
	if !ok {
		t.Fatalf("match_result is %T, want *plotbag.Result", out["match_result"])
	}
	if result.Summary.NCandidates != 2 {
		t.Fatalf("expected 2 candidates, got %d", result.Summary.NCandidates)
	}
	if result.Summary.NFullMatches != 1 || result.Summary.NPartialMatches != 1 {
		t.Errorf("expected 1 full and 1 partial match, got %d and %d",
			result.Summary.NFullMatches, result.Summary.NPartialMatches)
	}
	if result.Matches[0].BagID != "NL.IMBAG.PAND.0001" {
		t.Errorf("best match = %s, want NL.IMBAG.PAND.0001", result.Matches[0].BagID)
	}

	info, ok := out["bag_query"].(*QueryInfo)
	if !ok {
		t.Fatalf("bag_query is %T, want *QueryInfo", out["bag_query"])
	}
	if info.FeatureCount != 3 {
		t.Errorf("FeatureCount = %d, want 3", info.FeatureCount)
	}
	if info.Collection != "pand" || info.SourceURL != srv.URL {
		t.Errorf("bag_query = %+v, want collection pand from %s", info, srv.URL)
	}
}

// A plot the caller also passed bag_buildings for still matches against PDOK:
// this process is the one that decides where the BAG features come from.
func TestRun_IgnoresCallerSuppliedBuildings(t *testing.T) {
	srv, _ := fakeWFS(t, []any{
		pand("NL.IMBAG.PAND.0001", 2019.0, polygonGeom(square(5.0005, 52.0005, 5.0008, 52.0008))),
	})
	useWFS(t, srv.URL)

	out, err := Run(map[string]any{
		"plot": testPlot(),
		"bag_buildings": []any{
			pand("CALLER.0001", 1900.0, polygonGeom(square(5.0001, 52.0001, 5.0019, 52.0019))),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out["match_result"].(*plotbag.Result)
	if len(result.Matches) != 1 || result.Matches[0].BagID != "NL.IMBAG.PAND.0001" {
		t.Fatalf("expected only the PDOK pand, got %+v", result.Matches)
	}
}

func TestRun_PassesRatiosThrough(t *testing.T) {
	srv, _ := fakeWFS(t, []any{
		// Half of this pand lies inside the plot.
		pand("NL.IMBAG.PAND.0002", 2020.0, polygonGeom(square(5.0015, 52.0010, 5.0025, 52.0013))),
	})
	useWFS(t, srv.URL)

	// A threshold below the actual overlap turns the partial match into a full one.
	out, err := Run(map[string]any{
		"plot":                 testPlot(),
		"min_overlap_ratio":    0.1,
		"full_match_threshold": 0.4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out["match_result"].(*plotbag.Result)
	if result.Summary.NFullMatches != 1 {
		t.Fatalf("expected a full match with threshold 0.4, got %+v", result.Summary)
	}

	// Raising the minimum overlap above it drops the candidate entirely.
	out, err = Run(map[string]any{"plot": testPlot(), "min_overlap_ratio": 0.9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result = out["match_result"].(*plotbag.Result)
	if result.Summary.NCandidates != 0 || *result.Summary.FlagReason != plotbag.FlagNoBAGObject {
		t.Fatalf("expected no candidates, got %+v", result.Summary)
	}
}

func TestRun_InvalidInput(t *testing.T) {
	srv, urls := fakeWFS(t, []any{})
	useWFS(t, srv.URL)

	tests := []struct {
		name   string
		inputs map[string]any
	}{
		{"no plot", map[string]any{}},
		{"plot without plan_dataid", map[string]any{
			"plot": map[string]any{"geometry": polygonGeom(square(5, 52, 5.1, 52.1))},
		}},
		{"plot geometry is a point", map[string]any{
			"plot": map[string]any{
				"plan_dataid": "p1",
				"geometry":    map[string]any{"type": "Point", "coordinates": []any{5.0, 52.0}},
			},
		}},
		{"ratio out of range", map[string]any{"plot": testPlot(), "min_overlap_ratio": 1.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(tt.inputs)
			var ie *plotbag.InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error %v is not a *plotbag.InputError", err)
			}
		})
	}
	// Bad input must be rejected before PDOK is called, except for the ratio
	// check, which plotbag does after the fetch.
	if len(*urls) > 1 {
		t.Errorf("expected at most 1 upstream request for invalid input, got %d", len(*urls))
	}
}

func TestRun_UpstreamFailureIsNotAnInputError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	useWFS(t, srv.URL)

	_, err := Run(map[string]any{"plot": testPlot()})
	var ie *plotbag.InputError
	if errors.As(err, &ie) {
		t.Fatal("an upstream failure must not be reported as invalid input")
	}
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %v is not an *UpstreamError", err)
	}
}
