package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	tests := []struct {
		name        string
		allowed     []string
		method      string
		origin      string
		preflight   bool
		wantStatus  int
		wantAllowed string
	}{
		{"preflight any origin", []string{"*"}, http.MethodOptions, "null", true, http.StatusNoContent, "*"},
		{"simple request any origin", []string{"*"}, http.MethodGet, "http://localhost:5500", false, http.StatusTeapot, "*"},
		{"listed origin echoed", []string{"null"}, http.MethodPost, "null", false, http.StatusTeapot, "null"},
		{"unlisted origin", []string{"https://a.example"}, http.MethodOptions, "https://b.example", true, http.StatusTeapot, ""},
		{"no origin header", []string{"*"}, http.MethodGet, "", false, http.StatusTeapot, ""},
		{"plain OPTIONS passes through", []string{"*"}, http.MethodOptions, "null", false, http.StatusTeapot, "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/processes/x/execution", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight {
				req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			}
			rec := httptest.NewRecorder()
			CORS(tt.allowed, next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllowed {
				t.Errorf("Allow-Origin = %q, want %q", got, tt.wantAllowed)
			}
		})
	}
}

func TestParseOrigins(t *testing.T) {
	got := ParseOrigins(" null, http://localhost:5500 ,,")
	if len(got) != 2 || got[0] != "null" || got[1] != "http://localhost:5500" {
		t.Errorf("ParseOrigins = %q", got)
	}
}
