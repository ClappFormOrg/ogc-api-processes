package plotbag

// WGS84 → RD New (EPSG:28992) conversion using the polynomial approximation of
// Schreutelkamp & Strang van Hees ("Benaderingsformules voor de transformatie
// tussen RD- en WGS84-kaartcoördinaten", 2001). Accuracy is well under a metre
// inside the Netherlands, which is more than enough here: the projection is only
// used so that polygon areas are in m² instead of degrees, and the matching
// works on area *ratios*, which are insensitive to small absolute offsets.
//
// The Python notebook this process was ported from used pyproj for the same
// transformation; results agree to within rounding at 4 decimals.

const (
	rdPhi0 = 52.15517440 // latitude of Amersfoort reference point (WGS84)
	rdLam0 = 5.38720621  // longitude of Amersfoort reference point (WGS84)
	rdX0   = 155000.0
	rdY0   = 463000.0
)

type rdTerm struct {
	p, q int
	c    float64
}

var rdXTerms = []rdTerm{
	{0, 1, 190094.945}, {1, 1, -11832.228}, {2, 1, -114.221},
	{0, 3, -32.391}, {1, 0, -0.705}, {3, 1, -2.340},
	{1, 3, -0.608}, {0, 2, -0.008}, {2, 3, 0.148},
}

var rdYTerms = []rdTerm{
	{1, 0, 309056.544}, {0, 2, 3638.893}, {2, 0, 73.077},
	{1, 2, -157.984}, {3, 0, 59.788}, {0, 1, 0.433},
	{2, 2, -6.439}, {1, 1, -0.032}, {0, 4, 0.092},
	{1, 4, -0.054},
}

// toRD converts a WGS84 lon/lat pair (GeoJSON axis order) to RD New x/y in metres.
func toRD(lon, lat float64) (x, y float64) {
	dPhi := 0.36 * (lat - rdPhi0)
	dLam := 0.36 * (lon - rdLam0)
	x, y = rdX0, rdY0
	for _, t := range rdXTerms {
		x += t.c * ipow(dPhi, t.p) * ipow(dLam, t.q)
	}
	for _, t := range rdYTerms {
		y += t.c * ipow(dPhi, t.p) * ipow(dLam, t.q)
	}
	return x, y
}

func ipow(v float64, n int) float64 {
	r := 1.0
	for range n {
		r *= v
	}
	return r
}
