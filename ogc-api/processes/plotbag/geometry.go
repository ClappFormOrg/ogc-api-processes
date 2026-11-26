package plotbag

import (
	"errors"
	"fmt"
	"math"
)

// Minimal planar polygon toolkit: just enough to parse GeoJSON (Multi)Polygons,
// check and repair self-intersecting rings, and compute the area of the
// intersection of two polygons. It replaces the shapely calls in the original
// notebook so the service stays standard-library only.

type point struct{ x, y float64 }

// ring is an open vertex list: the closing vertex is not repeated.
type ring []point

// polygon is one exterior ring plus zero or more holes. After normalisation the
// exterior is counter-clockwise and holes are clockwise, so summing the signed
// areas of all rings gives the polygon area.
type polygon struct {
	outer ring
	holes []ring
}

type multiPolygon []polygon

func (m multiPolygon) isEmpty() bool { return len(m) == 0 }

func (m multiPolygon) rings() []ring {
	var rs []ring
	for _, p := range m {
		rs = append(rs, p.outer)
		rs = append(rs, p.holes...)
	}
	return rs
}

// area returns the signed-ring area sum, i.e. the polygon area for normalised input.
func (m multiPolygon) area() float64 {
	a := 0.0
	for _, r := range m.rings() {
		a += signedArea(r)
	}
	return a
}

var errNotPolygonal = errors.New("geometry is not a Polygon or MultiPolygon")

// parseGeoJSON reads a GeoJSON Polygon or MultiPolygon (lon/lat) and projects
// every vertex to RD New. Rings come back with orientation normalised.
func parseGeoJSON(g any) (multiPolygon, error) { return parseGeoJSONAs(g, true) }

// parseGeoJSONAs is parseGeoJSON with the projection made optional. Left out,
// the vertices stay in WGS84 lon/lat, which is what lets a step emit repaired
// geometry as GeoJSON for the next step: the RD transform in rd.go is forward
// only, so projected geometry cannot be turned back into lon/lat.
func parseGeoJSONAs(g any, project bool) (multiPolygon, error) {
	obj, ok := g.(map[string]any)
	if !ok {
		return nil, errors.New("geometry must be a GeoJSON object")
	}
	coords, ok := obj["coordinates"].([]any)
	if !ok {
		return nil, errors.New("geometry has no coordinates array")
	}
	switch obj["type"] {
	case "Polygon":
		if len(coords) == 0 {
			return nil, nil
		}
		p, err := parsePolygon(coords, project)
		if err != nil {
			return nil, err
		}
		return multiPolygon{p}, nil
	case "MultiPolygon":
		var mp multiPolygon
		for i, pc := range coords {
			pcs, ok := pc.([]any)
			if !ok {
				return nil, fmt.Errorf("polygon %d is not an array", i)
			}
			if len(pcs) == 0 {
				continue
			}
			p, err := parsePolygon(pcs, project)
			if err != nil {
				return nil, fmt.Errorf("polygon %d: %w", i, err)
			}
			mp = append(mp, p)
		}
		return mp, nil
	default:
		return nil, errNotPolygonal
	}
}

func parsePolygon(coords []any, project bool) (polygon, error) {
	var p polygon
	for i, rc := range coords {
		r, err := parseRing(rc, project)
		if err != nil {
			return polygon{}, fmt.Errorf("ring %d: %w", i, err)
		}
		if i == 0 {
			p.outer = orient(r, true)
		} else {
			p.holes = append(p.holes, orient(r, false))
		}
	}
	return p, nil
}

func parseRing(rc any, project bool) (ring, error) {
	positions, ok := rc.([]any)
	if !ok {
		return nil, errors.New("ring is not an array of positions")
	}
	r := make(ring, 0, len(positions))
	for _, pos := range positions {
		xy, ok := pos.([]any)
		if !ok || len(xy) < 2 {
			return nil, errors.New("position must be an array of at least 2 numbers")
		}
		lon, ok1 := xy[0].(float64)
		lat, ok2 := xy[1].(float64)
		if !ok1 || !ok2 || math.IsNaN(lon) || math.IsNaN(lat) {
			return nil, errors.New("position coordinates must be numbers")
		}
		x, y := lon, lat
		if project {
			x, y = toRD(lon, lat)
		}
		pt := point{x, y}
		if len(r) > 0 && r[len(r)-1] == pt {
			continue // drop consecutive duplicates
		}
		r = append(r, pt)
	}
	if len(r) > 1 && r[0] == r[len(r)-1] {
		r = r[:len(r)-1]
	}
	if len(r) < 3 {
		return nil, errors.New("a linear ring requires at least 3 distinct positions")
	}
	return r, nil
}

func signedArea(r ring) float64 {
	a := 0.0
	for i := range r {
		j := (i + 1) % len(r)
		a += r[i].x*r[j].y - r[j].x*r[i].y
	}
	return a / 2
}

// orient returns r with counter-clockwise (ccw=true) or clockwise orientation.
func orient(r ring, ccw bool) ring {
	if (signedArea(r) > 0) == ccw {
		return r
	}
	out := make(ring, len(r))
	for i, p := range r {
		out[len(r)-1-i] = p
	}
	return out
}

// ---------- validity & repair ----------

// isValid reports whether every ring is simple (no self-intersections) and
// encloses a non-zero area. Interactions between different rings (a hole
// crossing its shell, overlapping multipolygon parts) are not checked; the
// source data (plan polygons) is single-ring in practice.
func (m multiPolygon) isValid() bool {
	for _, r := range m.rings() {
		if math.Abs(signedArea(r)) == 0 || len(selfIntersections(r)) > 0 {
			return false
		}
	}
	return true
}

// crossing is a proper intersection between segments i and j of a ring, at
// parameter ti along segment i and tj along segment j.
type crossing struct {
	i, j   int
	ti, tj float64
	p      point
}

// selfIntersections finds intersections between non-adjacent segments of r.
func selfIntersections(r ring) []crossing {
	n := len(r)
	var out []crossing
	for i := range n {
		a, b := r[i], r[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i+1 || (i == 0 && j == n-1) {
				continue // adjacent segments share a vertex
			}
			c, d := r[j], r[(j+1)%n]
			if ti, tj, ok := segmentIntersection(a, b, c, d); ok {
				out = append(out, crossing{i, j, ti, tj, lerp(a, b, ti)})
			}
		}
	}
	return out
}

// segmentIntersection returns the parameters of the intersection point of
// segments ab and cd, if they intersect (endpoints included). Collinear
// overlapping segments are reported at the start of the overlap.
func segmentIntersection(a, b, c, d point) (ti, tj float64, ok bool) {
	r := point{b.x - a.x, b.y - a.y}
	s := point{d.x - c.x, d.y - c.y}
	denom := cross(r, s)
	qp := point{c.x - a.x, c.y - a.y}
	if denom == 0 {
		if cross(qp, r) != 0 {
			return 0, 0, false // parallel, not collinear
		}
		rr := r.x*r.x + r.y*r.y
		if rr == 0 {
			return 0, 0, false
		}
		t0 := (qp.x*r.x + qp.y*r.y) / rr
		t1 := t0 + (s.x*r.x+s.y*r.y)/rr
		lo, hi := math.Min(t0, t1), math.Max(t0, t1)
		if hi < 0 || lo > 1 {
			return 0, 0, false
		}
		t := math.Max(lo, 0)
		p := lerp(a, b, t)
		ss := s.x*s.x + s.y*s.y
		return t, ((p.x-c.x)*s.x + (p.y-c.y)*s.y) / ss, true
	}
	t := cross(qp, s) / denom
	u := cross(qp, r) / denom
	if t < 0 || t > 1 || u < 0 || u > 1 {
		return 0, 0, false
	}
	return t, u, true
}

func cross(a, b point) float64 { return a.x*b.y - a.y*b.x }

func lerp(a, b point, t float64) point {
	return point{a.x + t*(b.x-a.x), a.y + t*(b.y-a.y)}
}

// repair splits every self-intersecting ring at its intersection points into
// simple loops and keeps the loops that enclose area. For a "bowtie" this
// yields both lobes (like shapely.make_valid / PostGIS ST_MakeValid), where the
// notebook's buffer(0) kept only one of them. Holes are re-attached to the
// shell loop that contains them.
func (m multiPolygon) repair() multiPolygon {
	var out multiPolygon
	for _, p := range m {
		var shells []polygon
		for _, loop := range splitLoops(p.outer) {
			shells = append(shells, polygon{outer: orient(loop, true)})
		}
		for _, h := range p.holes {
			for _, loop := range splitLoops(h) {
				for k := range shells {
					if pointInRing(loop[0], shells[k].outer) {
						shells[k].holes = append(shells[k].holes, orient(loop, false))
						break
					}
				}
			}
		}
		out = append(out, shells...)
	}
	return out
}

// splitLoops nodes r at its self-intersections and peels off closed loops.
func splitLoops(r ring) []ring {
	// Insert intersection points into their segments, ordered along the segment.
	type insert struct {
		t float64
		p point
	}
	inserts := make(map[int][]insert)
	for _, c := range selfIntersections(r) {
		inserts[c.i] = append(inserts[c.i], insert{c.ti, c.p})
		inserts[c.j] = append(inserts[c.j], insert{c.tj, c.p})
	}
	var noded ring
	for i, v := range r {
		noded = append(noded, v)
		ins := inserts[i]
		for a := 1; a < len(ins); a++ { // insertion sort, lists are tiny
			for b := a; b > 0 && ins[b].t < ins[b-1].t; b-- {
				ins[b], ins[b-1] = ins[b-1], ins[b]
			}
		}
		for _, in := range ins {
			if in.p != noded[len(noded)-1] {
				noded = append(noded, in.p)
			}
		}
	}

	// Walk the noded ring; whenever a vertex repeats, the path since its last
	// occurrence is a closed loop.
	var loops []ring
	var stack ring
	seen := make(map[point]int)
	for _, v := range append(noded, noded[0]) {
		if k, ok := seen[v]; ok {
			loop := append(ring(nil), stack[k:]...)
			for _, q := range loop[1:] {
				delete(seen, q)
			}
			stack = stack[:k+1]
			if len(loop) >= 3 && math.Abs(signedArea(loop)) > 0 {
				loops = append(loops, loop)
			}
			continue
		}
		seen[v] = len(stack)
		stack = append(stack, v)
	}
	return loops
}

func pointInRing(p point, r ring) bool {
	in := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			in = !in
		}
	}
	return in
}

// ---------- intersection area ----------

// intersectionArea returns area(a ∩ b).
//
// It uses winding numbers instead of constructing the intersection geometry:
// with shells counter-clockwise and holes clockwise, a polygon's indicator
// function is the sum of its rings' winding numbers, and a ring's winding
// number is the signed sum of the triangles of a fan from its first vertex.
// Each fan triangle is convex, so clipping a ring of the other polygon to it
// with Sutherland–Hodgman is exact, and the signed area of the clipped ring is
// the integral of that ring's winding number over the triangle. This needs no
// triangulation of non-convex shapes and handles holes for free.
func intersectionArea(a, b multiPolygon) float64 {
	total := 0.0
	for _, rb := range b.rings() {
		for i := 1; i+1 < len(rb); i++ {
			tri := ring{rb[0], rb[i], rb[i+1]}
			sa := signedArea(tri)
			if sa == 0 {
				continue
			}
			sign := 1.0
			if sa < 0 {
				sign = -1
				tri = ring{rb[0], rb[i+1], rb[i]}
			}
			for _, ra := range a.rings() {
				total += sign * signedArea(clipToConvex(ra, tri))
			}
		}
	}
	return total
}

// clipToConvex clips subject against the counter-clockwise convex polygon clip
// (Sutherland–Hodgman).
func clipToConvex(subject, clip ring) ring {
	out := subject
	for i := range clip {
		if len(out) == 0 {
			break
		}
		e0, e1 := clip[i], clip[(i+1)%len(clip)]
		inside := func(p point) bool {
			return cross(point{e1.x - e0.x, e1.y - e0.y}, point{p.x - e0.x, p.y - e0.y}) >= 0
		}
		in := out
		out = make(ring, 0, len(in)+2)
		for k := range in {
			cur, prev := in[k], in[(k+len(in)-1)%len(in)]
			curIn, prevIn := inside(cur), inside(prev)
			if curIn != prevIn {
				out = append(out, lineIntersect(prev, cur, e0, e1))
			}
			if curIn {
				out = append(out, cur)
			}
		}
	}
	return out
}

// lineIntersect returns the intersection of segment pq with the infinite line e0e1.
func lineIntersect(p, q, e0, e1 point) point {
	e := point{e1.x - e0.x, e1.y - e0.y}
	d := point{q.x - p.x, q.y - p.y}
	denom := cross(e, d)
	if denom == 0 {
		return p
	}
	t := cross(e, point{e0.x - p.x, e0.y - p.y}) / denom
	return lerp(p, q, t)
}

// toGeoJSON renders the multiPolygon back as a GeoJSON geometry. The caller is
// responsible for having parsed it unprojected (parseGeoJSONAs with project
// false): rd.go transforms lon/lat to RD New and not back, so a projected
// multiPolygon would serialise as RD metres labelled as GeoJSON.
//
// Rings are re-closed, since ring drops the repeated closing vertex on parse.
func (m multiPolygon) toGeoJSON() map[string]any {
	polys := make([]any, 0, len(m))
	for _, p := range m {
		rings := make([]any, 0, 1+len(p.holes))
		rings = append(rings, closedRingCoords(p.outer))
		for _, h := range p.holes {
			rings = append(rings, closedRingCoords(h))
		}
		polys = append(polys, rings)
	}
	if len(polys) == 1 {
		return map[string]any{"type": "Polygon", "coordinates": polys[0]}
	}
	return map[string]any{"type": "MultiPolygon", "coordinates": polys}
}

func closedRingCoords(r ring) []any {
	out := make([]any, 0, len(r)+1)
	for _, p := range r {
		out = append(out, []any{p.x, p.y})
	}
	if len(r) > 0 {
		out = append(out, []any{r[0].x, r[0].y})
	}
	return out
}
