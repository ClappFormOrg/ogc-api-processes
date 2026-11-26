package main

import (
	"log/slog"
	"net/http"
	"ogc-api/handlers"
	"ogc-api/processes/plotbag/pdok"
	"ogc-api/store"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost" + addr
	}
	handlers.BaseURL = baseURL

	handlers.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	jobMaxAge := 48 * time.Hour
	if h := os.Getenv("JOB_MAX_AGE_HOURS"); h != "" {
		if hours, err := strconv.Atoi(h); err == nil && hours > 0 {
			jobMaxAge = time.Duration(hours) * time.Hour
		}
	}

	// PDOK_BAG_WFS_URL points plot-bag-matching-pdok at a different BAG WFS
	// (a mirror or a test double); the public PDOK service is the default.
	if u := os.Getenv("PDOK_BAG_WFS_URL"); u != "" {
		pdok.WFSURL = u
	}
	handlers.Logger.Info("pdok bag wfs", "url", pdok.WFSURL)

	s := store.New()
	stopCleanup := s.StartCleanup(1*time.Hour, jobMaxAge)
	defer stopCleanup()

	ph := handlers.NewProcessHandlers(s)
	jh := &handlers.JobHandlers{Store: s}

	mux := http.NewServeMux()

	// Core
	mux.HandleFunc("/", handlers.LandingPage)
	mux.HandleFunc("/api", handlers.OpenAPI)
	mux.HandleFunc("/conformance", handlers.Conformance)

	// Processes: need custom routing for GET vs POST and /execution suffix
	mux.HandleFunc("/processes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			handlers.WriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		ph.List(w, r)
	})
	mux.HandleFunc("/processes/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/processes/")
		switch {
		case strings.HasSuffix(path, "/execution"):
			if r.Method != http.MethodPost {
				handlers.WriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			ph.Execute(w, r)
		default:
			if r.Method != http.MethodGet {
				handlers.WriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			ph.Describe(w, r)
		}
	})

	// Jobs
	mux.HandleFunc("/jobs", jh.Route)
	mux.HandleFunc("/jobs/", jh.Route)

	logger := handlers.Logger
	logger.Info("server starting", "addr", addr)
	logger.Info("landing page", "url", handlers.BaseURL+"/")
	logger.Info("api definition", "url", handlers.BaseURL+"/api")
	logger.Info("conformance", "url", handlers.BaseURL+"/conformance")
	logger.Info("processes", "url", handlers.BaseURL+"/processes")
	logger.Info("jobs", "url", handlers.BaseURL+"/jobs")

	// CORS_ALLOWED_ORIGINS: comma-separated origins, "*" (default) for any.
	// Use "null" to allow pages opened from file:// only.
	corsOrigins := []string{"*"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		corsOrigins = handlers.ParseOrigins(v)
	}
	logger.Info("cors", "allowed_origins", corsOrigins)

	if err := http.ListenAndServe(addr, handlers.CORS(corsOrigins, mux)); err != nil {
		logger.Error("server stopped", "err", err)
	}
}
