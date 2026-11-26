package handlers

import (
	"net/http"
	"ogc-api/models"
)

// BaseURL is set at startup and used to build self-referencing links.
var BaseURL = "http://localhost:8080"

// LandingPage handles GET / (OGC Requirement 1).
func LandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, models.LandingPage{
		Title:       "OGC Processes API",
		Description: "OGC API - Processes - Part 1: Core 2.0 service.",
		Links: []models.Link{
			{Href: BaseURL + "/", Rel: "self", Type: "application/json", Title: "This document"},
			{Href: BaseURL + "/api", Rel: "service-desc", Type: openAPIContentType, Title: "API definition (OpenAPI 3.0)"},
			{Href: BaseURL + "/conformance", Rel: relOGCPrefix + "conformance", Type: "application/json", Title: "Conformance classes"},
			{Href: BaseURL + "/processes", Rel: relOGCPrefix + "processes", Type: "application/json", Title: "Processes"},
			{Href: BaseURL + "/jobs", Rel: relOGCPrefix + "job-list", Type: "application/json", Title: "Jobs"},
		},
	})
}

// conformancePrefix is the OGC API - Processes 2.0 conformance class base URI.
const conformancePrefix = "https://www.opengis.net/spec/ogcapi-processes-1/2.0/conf/"

// Conformance handles GET /conformance (/req/core/conformance-success).
// oas30/oas31 are not claimed: /api serves a generated OpenAPI document but it
// has not been validated against those conformance classes.
func Conformance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, models.Conformance{ConformsTo: conformanceClasses()})
}

// conformanceClasses lists the conformance classes this server implements.
func conformanceClasses() []string {
	return []string{
		conformancePrefix + "core",
		conformancePrefix + "ogc-process-description",
		conformancePrefix + "json",
		conformancePrefix + "job-list",
		conformancePrefix + "callback",
		conformancePrefix + "dismiss",

		// Part 3: Workflows and Chaining. Only nested-processes is
		// implemented; remote processes, collection input/output, field
		// modifiers, deployable workflows, CWL and openEO are not.
		conformanceP3Prefix + "nested-processes",
	}
}
