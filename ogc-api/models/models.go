package models

import "time"

// Link represents an OGC API link (RFC 8288).
type Link struct {
	Href     string `json:"href"`
	Rel      string `json:"rel,omitempty"`
	Type     string `json:"type,omitempty"`
	Hreflang string `json:"hreflang,omitempty"`
	Title    string `json:"title,omitempty"`
}

// LandingPage is the root response (OGC Requirement 1).
type LandingPage struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Links       []Link `json:"links"`
}

// Conformance declares supported conformance classes (OGC Requirement 2).
type Conformance struct {
	ConformsTo []string `json:"conformsTo"`
}

// ProcessSummary is a short description returned in process lists.
// outputTransmission was removed in OGC API - Processes 2.0.
type ProcessSummary struct {
	ID                string   `json:"id"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version"`
	Keywords          []string `json:"keywords,omitempty"`
	JobControlOptions []string `json:"jobControlOptions,omitempty"`
	Links             []Link   `json:"links,omitempty"`
}

// ProcessList is the response for GET /processes.
type ProcessList struct {
	Processes []ProcessSummary `json:"processes"`
	Links     []Link           `json:"links,omitempty"`
}

// InputDescription describes a single process input.
type InputDescription struct {
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Schema      interface{} `json:"schema"`
	// MinOccurs is always emitted: schemaAndOccurrences defaults an absent
	// minOccurs to 1, so optional inputs must say 0 explicitly.
	MinOccurs int         `json:"minOccurs"`
	MaxOccurs interface{} `json:"maxOccurs,omitempty"` // int or "unbounded"
	// ValuePassing lists how the input may be supplied ("byValue", "byReference").
	// Absent means both, so it must be set when by-reference is unsupported.
	ValuePassing []string `json:"valuePassing,omitempty"`
}

// OutputDescription describes a single process output.
type OutputDescription struct {
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Schema      interface{} `json:"schema"`
}

// ProcessDescription is the full process description (OGC Requirement 30).
type ProcessDescription struct {
	ProcessSummary
	Inputs  map[string]InputDescription  `json:"inputs,omitempty"`
	Outputs map[string]OutputDescription `json:"outputs,omitempty"`
}

// JobStatus values per OGC spec.
const (
	JobStatusAccepted   = "accepted"
	JobStatusRunning    = "running"
	JobStatusSuccessful = "successful"
	JobStatusFailed     = "failed"
	JobStatusDismissed  = "dismissed"
)

// ProcessingEntityType is the fixed statusInfo.processingEntityType value for
// jobs created through OGC API - Processes.
const ProcessingEntityType = "ogc-api-processes"

// StatusInfo represents a job's current status (statusInfo.yaml, 2.0).
// 2.0 renamed jobID → id and replaced type: "process" with processingEntityType.
type StatusInfo struct {
	ID                   string         `json:"id"`
	Status               string         `json:"status"`
	ProcessingEntityType string         `json:"processingEntityType"`
	ProcessID            string         `json:"processID,omitempty"` // absolute process URL (format: uri)
	Exception            *ErrorResponse `json:"exception,omitempty"` // set when the job failed
	Message              string         `json:"message,omitempty"`
	Created              *time.Time     `json:"created,omitempty"`
	Started              *time.Time     `json:"started,omitempty"`
	Finished             *time.Time     `json:"finished,omitempty"`
	Updated              *time.Time     `json:"updated,omitempty"`
	Progress             *int           `json:"progress,omitempty"`
	Links                []Link         `json:"links,omitempty"`
}

// JobList is the response for GET /jobs.
type JobList struct {
	Jobs  []StatusInfo `json:"jobs"`
	Links []Link       `json:"links,omitempty"`
}

// ExecutionRequest is the body for POST /processes/{id}/execution (execute.yaml, 2.0).
// 2.0 removed the "response" (raw/document) member: the response shape now
// follows from the number of requested outputs. Outputs distinguishes absent
// (nil → all outputs) from an empty object (→ 204 No Content).
type ExecutionRequest struct {
	Process    string                 `json:"process,omitempty"`
	Inputs     map[string]interface{} `json:"inputs,omitempty"`
	Outputs    map[string]Output      `json:"outputs"`
	Subscriber *Subscriber            `json:"subscriber,omitempty"`
}

// Output selects a process output and optionally its format (outputSelection.yaml).
// transmissionMode was removed in 2.0.
type Output struct {
	Format *Format `json:"format,omitempty"`
}

// Format specifies a media type.
type Format struct {
	MediaType string `json:"mediaType,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
}

// Subscriber holds callback URIs for async execution.
type Subscriber struct {
	SuccessURI    string `json:"successUri,omitempty"`
	InProgressURI string `json:"inProgressUri,omitempty"`
	FailedURI     string `json:"failedUri,omitempty"`
}

// ErrorResponse is a standard RFC 9457 Problem Details error body.
type ErrorResponse struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}
