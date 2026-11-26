package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
)

const openAPIContentType = "application/vnd.oai.openapi+json;version=3.0"

// ogcSchemasBase is where the OGC API - Processes 2.0 schemas live. The draft
// (18-062r3) is not yet published under schemas.opengis.net/ogcapi/processes/part1/2.0/,
// so the master branch of the standard's repository is the canonical source.
// Switch to schemas.opengis.net once the 2.0 schemas are published there.
const ogcSchemasBase = "https://raw.githubusercontent.com/opengeospatial/ogcapi-processes/master/openapi/schemas/"

// OpenAPI handles GET /api: an OpenAPI 3.0 definition linked from the landing
// page as service-desc. Request and response bodies reference the official
// OGC schemas, so tools such as the Geonovum ogc-checker can verify them;
// the process ID parameter is generated from processRegistry.
//
// It is OpenAPI 3.0 rather than 3.1 because validators for OGC API
// definitions (including the ogc-checker) only accept 3.0.x.
func OpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Content-Type", openAPIContentType)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(buildOpenAPI())
}

func buildOpenAPI() map[string]any {
	ref := func(kind, name string) map[string]any {
		return map[string]any{"$ref": "#/components/" + kind + "/" + name}
	}
	jsonResponse := func(desc, schema string) map[string]any {
		return map[string]any{
			"description": desc,
			"content": map[string]any{"application/json": map[string]any{
				"schema": ref("schemas", schema),
			}},
		}
	}
	problem := func(desc string) map[string]any {
		schema := map[string]any{"schema": ref("schemas", "exception")}
		return map[string]any{
			"description": desc,
			"content": map[string]any{
				"application/json": schema,
				problemContentType: schema,
			},
		}
	}
	queryParam := func(name, desc string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "description": desc, "required": false, "schema": schema}
	}
	strArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}

	processIDs := make([]string, 0, len(processDescriptions))
	for id := range processDescriptions {
		processIDs = append(processIDs, id)
	}
	sort.Strings(processIDs)

	outputs := queryParam("outputs", "Comma-separated list of output identifiers to include in the response.", strArray())
	outputs["style"] = "form"
	outputs["explode"] = false

	components := map[string]any{
		"schemas": map[string]any{
			"landingPage": map[string]any{"$ref": ogcSchemasBase + "common-core/landingPage.yaml"},
			"confClasses": map[string]any{"$ref": ogcSchemasBase + "common-core/confClasses.yaml"},
			"exception":   map[string]any{"$ref": ogcSchemasBase + "common-core/exception.yaml"},
			"processList": map[string]any{"$ref": ogcSchemasBase + "processes-core/processList.yaml"},
			"process":     map[string]any{"$ref": ogcSchemasBase + "processes-core/process.yaml"},
			"execute":     map[string]any{"$ref": ogcSchemasBase + "processes-core/execute.yaml"},
			"statusInfo":  map[string]any{"$ref": ogcSchemasBase + "processes-core/statusInfo.yaml"},
			"jobList":     map[string]any{"$ref": ogcSchemasBase + "processes-core/jobList.yaml"},
			"results":     map[string]any{"$ref": ogcSchemasBase + "processes-core/results.yaml"},
		},
		"parameters": map[string]any{
			"processID-path": map[string]any{
				"name": "processId", "in": "path", "required": true,
				"description": "Identifier of a process.",
				"schema":      map[string]any{"type": "string", "enum": processIDs},
			},
			"jobID": map[string]any{
				"name": "jobId", "in": "path", "required": true,
				"description": "Identifier of a job.",
				"schema":      map[string]any{"type": "string"},
			},
			"outputID": map[string]any{
				"name": "outputId", "in": "path", "required": true,
				"description": "Identifier of a process output.",
				"schema":      map[string]any{"type": "string"},
			},
			"limit": queryParam("limit", "Maximum number of items to return.",
				map[string]any{"type": "integer", "minimum": 1, "maximum": 10000, "default": 10}),
			"outputs": outputs,
		},
		"responses": map[string]any{
			"NotFound":         problem("The requested resource does not exist."),
			"InvalidParameter": problem("A query parameter or the request body has an invalid value."),
			"ServerError":      problem("A server error occurred, or the job failed."),
		},
	}

	jobIDParam := ref("parameters", "jobID")
	paths := map[string]any{
		"/": map[string]any{"get": map[string]any{
			"summary": "Landing page", "operationId": "getLandingPage",
			"responses": map[string]any{"200": jsonResponse("Landing page", "landingPage")},
		}},
		"/api": map[string]any{"get": map[string]any{
			"summary": "This API definition", "operationId": "getAPI",
			"responses": map[string]any{"200": map[string]any{
				"description": "OpenAPI definition",
				"content":     map[string]any{openAPIContentType: map[string]any{"schema": map[string]any{"type": "object"}}},
			}},
		}},
		"/conformance": map[string]any{"get": map[string]any{
			"summary": "Conformance classes", "operationId": "getConformance",
			"responses": map[string]any{"200": jsonResponse("Conformance declaration", "confClasses")},
		}},
		"/processes": map[string]any{"get": map[string]any{
			"summary": "List processes", "operationId": "getProcesses",
			"parameters": []any{ref("parameters", "limit")},
			"responses": map[string]any{
				"200": jsonResponse("Process list", "processList"),
				"400": ref("responses", "InvalidParameter"),
			},
		}},
		"/processes/{processId}": map[string]any{"get": map[string]any{
			"summary": "Describe a process", "operationId": "getProcessDescription",
			"parameters": []any{ref("parameters", "processID-path")},
			"responses": map[string]any{
				"200": jsonResponse("Process description", "process"),
				"404": ref("responses", "NotFound"),
			},
		}},
		"/processes/{processId}/execution": map[string]any{"post": map[string]any{
			"summary":     "Execute a process",
			"description": "Inputs are described by the process description (GET /processes/{processId}).",
			"operationId": "execute",
			"parameters": []any{
				ref("parameters", "processID-path"),
				map[string]any{
					"name": "Prefer", "in": "header", "required": false,
					"description": "respond-async or respond-sync (RFC 7240). Unknown preferences are ignored.",
					"schema":      map[string]any{"type": "string"},
				},
			},
			"requestBody": map[string]any{
				"description": "Execution request",
				"required":    true,
				"content": map[string]any{"application/json": map[string]any{
					"schema": ref("schemas", "execute"),
				}},
			},
			"callbacks": map[string]any{
				"jobCompleted": map[string]any{
					"{$request.body#/subscriber/successUri}": map[string]any{"post": map[string]any{
						"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{
							"schema": ref("schemas", "results"),
						}}},
						"responses": map[string]any{"200": map[string]any{"description": "Callback received"}},
					}},
				},
			},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Synchronous result: the raw value for one requested output, a results document for several.",
					"content": map[string]any{"application/json": map[string]any{
						"schema": ref("schemas", "results"),
					}},
				},
				"201": map[string]any{
					"description": "Job created (asynchronous execution)",
					"headers": map[string]any{"Location": map[string]any{
						"description": "URL of the job", "schema": map[string]any{"type": "string", "format": "uri"},
					}},
					"content": map[string]any{"application/json": map[string]any{
						"schema": ref("schemas", "statusInfo"),
					}},
				},
				"204": map[string]any{"description": "No outputs requested"},
				"400": ref("responses", "InvalidParameter"),
				"404": ref("responses", "NotFound"),
				"500": ref("responses", "ServerError"),
			},
		}},
		"/jobs": map[string]any{"get": map[string]any{
			"summary": "List jobs", "operationId": "getJobs",
			"parameters": []any{
				ref("parameters", "limit"),
				queryParam("processID", "Only jobs of these processes (IDs or URLs).", strArray()),
				queryParam("status", "Only jobs with these statuses.", map[string]any{
					"type": "array", "items": map[string]any{"type": "string", "enum": validStatuses},
				}),
				queryParam("type", "Only jobs of these types.", map[string]any{
					"type": "array", "items": map[string]any{"type": "string", "enum": []string{"process"}},
				}),
				queryParam("datetime", "RFC 3339 instant or interval matched against the job creation time.", map[string]any{"type": "string"}),
				queryParam("minDuration", "Minimum job duration in seconds.", map[string]any{"type": "integer", "minimum": 0}),
				queryParam("maxDuration", "Maximum job duration in seconds.", map[string]any{"type": "integer", "minimum": 0}),
			},
			"responses": map[string]any{
				"200": jsonResponse("Job list", "jobList"),
				"400": ref("responses", "InvalidParameter"),
			},
		}},
		"/jobs/{jobId}": map[string]any{
			"get": map[string]any{
				"summary": "Job status", "operationId": "getStatus",
				"parameters": []any{jobIDParam},
				"responses": map[string]any{
					"200": jsonResponse("Status of the job", "statusInfo"),
					"404": ref("responses", "NotFound"),
				},
			},
			"delete": map[string]any{
				"summary": "Dismiss a job", "operationId": "dismiss",
				"parameters": []any{jobIDParam},
				"responses": map[string]any{
					"200": jsonResponse("Status of the dismissed job", "statusInfo"),
					"404": ref("responses", "NotFound"),
				},
			},
		},
		"/jobs/{jobId}/results": map[string]any{"get": map[string]any{
			"summary": "Job results", "operationId": "getResults",
			"parameters": []any{jobIDParam, ref("parameters", "outputs")},
			"responses": map[string]any{
				"200": jsonResponse("Results document", "results"),
				"400": ref("responses", "InvalidParameter"),
				"404": ref("responses", "NotFound"),
				"500": ref("responses", "ServerError"),
			},
		}},
	}

	singleResult := func(opID string) map[string]any {
		return map[string]any{"get": map[string]any{
			"summary": "Value of one job output", "operationId": opID,
			"parameters": []any{jobIDParam, ref("parameters", "outputID")},
			"responses": map[string]any{
				"200": map[string]any{
					"description": "The raw output value",
					"headers": map[string]any{headerOutputValuesCount: map[string]any{
						"description": "Number of values of the output", "schema": map[string]any{"type": "integer"},
					}},
					"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{}}},
				},
				"400": ref("responses", "InvalidParameter"),
				"404": ref("responses", "NotFound"),
				"500": ref("responses", "ServerError"),
			},
		}}
	}
	paths["/jobs/{jobId}/results/{outputId}"] = singleResult("getResult")
	paths["/jobs/{jobId}/results/{outputId}/0"] = singleResult("getResultFirstValue")

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "OGC Processes API",
			"version": "2.0.0",
			"description": "OGC API - Processes - Part 1: Core 2.0 (draft) service. Clients may pass an X-Api-Key " +
				"header, which is forwarded to upstream services that require it.",
		},
		"servers":    []any{map[string]any{"url": BaseURL}},
		"paths":      paths,
		"components": components,
	}
}
