package handlers

import (
	"net/http"
	"slices"
	"strings"
)

// corsAllowHeaders are the request headers browser clients may send: the
// execute body type, the OGC Prefer header for sync/async and the upstream key.
const corsAllowHeaders = "Accept, Content-Type, Prefer, X-Api-Key"

// corsExposeHeaders are the response headers browser clients need to read,
// e.g. Location of a newly created job (cross-origin JS cannot see it otherwise).
const corsExposeHeaders = "Location, Preference-Applied, OGC-Output-Values-Count, Link"

// CORS wraps next so browser clients on other origins (including file://,
// which sends Origin: null) can call the API. allowed is a list of origins;
// "*" allows any origin. Preflight OPTIONS requests are answered directly.
func CORS(allowed []string, next http.Handler) http.Handler {
	anyOrigin := slices.Contains(allowed, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (anyOrigin || slices.Contains(allowed, origin)) {
			h := w.Header()
			if anyOrigin {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ParseOrigins splits a comma-separated origin list, e.g. from CORS_ALLOWED_ORIGINS.
func ParseOrigins(v string) []string {
	var out []string
	for _, o := range strings.Split(v, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
