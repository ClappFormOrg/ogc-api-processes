# OGC API - Processes

A Go implementation of the [OGC API - Processes - Part 1: Core 2.0](https://docs.ogc.org/DRAFTS/18-062r3.html) draft standard, developed for the Geonovum DTaaS Testbed 2026, phase 2.

## Overview

This server exposes geospatial processing capabilities through a standards-compliant REST API. Clients can discover available processes, submit execution requests (synchronously or asynchronously), and retrieve job results.

The API declares these conformance classes (prefix `https://www.opengis.net/spec/`):

- `ogcapi-processes-1/2.0/conf/core`
- `ogcapi-processes-1/2.0/conf/ogc-process-description`
- `ogcapi-processes-1/2.0/conf/json`
- `ogcapi-processes-1/2.0/conf/job-list`
- `ogcapi-processes-1/2.0/conf/callback`
- `ogcapi-processes-1/2.0/conf/dismiss`
- `ogcapi-processes-3/0.0/conf/nested-processes`

> **Part 3 scope.** Only `nested-processes` of [OGC API - Processes - Part 3: Workflows and
> Chaining](https://docs.ogc.org/DRAFTS/21-009.html) is implemented. The other seven classes
> (`remote-core-processes`, `remote-collections`, `input-fields-modifiers`,
> `output-fields-modifiers`, `deployable-workflows`, `cwl-workflows`, `openeo-workflows`) are
> **not**. Part 3 is a Candidate SWG Draft, not an adopted standard, which is why its
> conformance URIs carry version `0.0` - expect them to change.

> **Note:** `/api` serves an OpenAPI 3.0.3 definition (linked as `service-desc`) whose request and response bodies reference the official OGC API - Processes 2.0 schemas. It is 3.0 rather than 3.1 because OGC API validators, including the Geonovum ogc-checker, only accept 3.0.x. `oas30` conformance is not claimed because the document has not been validated against that class.

### Validating with the Geonovum ogc-checker

```bash
npx @geonovum/ogc-checker@latest validate --standard ogc-api-processes --version 2.0.0 --input http://localhost:8080/api
```

Or open the [web checker](https://geonovum.github.io/ogc-checker/), pick OGC API - Processes 2.0.0 and point it at your `/api` URL (CORS is enabled by default). The checker lints the API definition, not live responses. The draft's own `processes-core/value.yaml` and `valueNoObject.yaml` currently contain broken `$ref`s (to `cql2.yaml`, `geometryGeoJSON.yaml` and missing CQL2 definitions), which the checker reports as `invalid-ref` errors; they are not caused by this server.

### Breaking changes from 1.0

- Job status uses `id` (was `jobID`) and `processingEntityType: "ogc-api-processes"` (was `type: "process"`). `processID` is the absolute process URL (e.g. `https://…/processes/plot-bag-matching`); the `processID` filter on `/jobs` accepts the URL or the bare ID. A failed job carries an `exception` problem object.
- The `response` (`raw`/`document`) member of the execute request is gone. The response shape follows from the number of requested outputs (see [Execution Modes](#execution-modes)). `transmissionMode` and `outputTransmission` are gone too.
- Conformance, link relation and exception URIs use `https://`, and errors are `application/problem+json`.
- `DELETE /jobs/{id}` on a finished or already-dismissed job removes the job and its results (a repeat DELETE returns 200, then 404).
- A failed synchronous execution now returns an error status (400 for invalid input, 502 for upstream failures, otherwise 500) instead of `200` with an `error` output. Its problem `type` is `urn:ogc-api:exception:invalid-input`, `…:upstream-failure` or `…:process-failed`.
- Inputs are validated against their full JSON Schema (nested `required`, `properties`, `items`, `enum`, `minimum`/`maximum`, `oneOf`, type arrays), not just the top-level type. By-reference inputs (`{"href": …}`) are rejected; every input declares `valuePassing: ["byValue"]` and an explicit `minOccurs`.
- A `respond-async`/`respond-sync` preference the process can't honour falls back to the supported mode instead of returning `501`. Unknown preferences are ignored.
- `405` responses carry an `Allow` header.

For the full architecture — layer diagrams, request lifecycles, the design decisions behind each choice, known spec deviations, and a step-by-step guide to reproducing this setup for another project — see [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Project Structure

```
ogc-api/
├── main.go              # Entry point — wires routes and starts the HTTP server
├── go.mod               # Go module definition (module: ogc-api, Go 1.22)
├── Makefile             # Build, run, test, format helpers
├── Dockerfile           # Multi-stage Docker build
├── handlers/
│   ├── core.go          # GET / and GET /conformance handlers
│   ├── openapi.go       # GET /api — generated OpenAPI 3.0 definition
│   ├── processes.go     # GET /processes, GET /processes/{id}, POST /processes/{id}/execution
│   ├── jobs.go          # GET /jobs, GET /jobs/{id}, GET /jobs/{id}/results[/{outputId}], DELETE /jobs/{id}
│   ├── functions.go     # Process registry — add new processes here
│   ├── helpers.go       # Shared HTTP utilities (writeJSON, writeProblem, pathParam, queryLimit)
│   └── logger.go        # Structured JSON logger (log/slog)
├── models/
│   └── models.go        # OGC data types (LandingPage, ProcessDescription, StatusInfo, …)
├── processes/
│   └── plotbag/         # plot-bag-matching: plot→BAG overlap matching (pure Go geometry, WGS84→RD)
│       ├── steps.go       # The same matching as five separately executable steps
│       └── pdok/          # plot-bag-matching-pdok: the same matching, BAG fetched from the PDOK WFS
└── store/
    └── store.go         # In-memory store for processes and jobs (thread-safe, with TTL cleanup)
```

---

## Requirements

- [Go 1.22+](https://go.dev/dl/) — for running locally
- [Docker](https://www.docker.com/) — for containerised deployment (optional)

No other external tools are required. The only dependency is `github.com/google/uuid` for job ID generation.

---

## Getting Started

### Run locally

```bash
cd ogc-api
go run .
```

Or use the Makefile:

```bash
make run
```

The server starts on **`http://localhost:8080`** by default.

### Run with Docker

```bash
cd ogc-api
docker compose up --build
```

### Build a binary

```bash
make build
# produces ogc-api.exe
./ogc-api.exe
```

### Web console

[ogc-api/web/index.html](ogc-api/web/index.html) is a dependency-free browser client for this API: open the
file directly (`file://`) or serve it from any static server, point the **Base URL**
field at the API and press **Connect**.

It lists the registered processes, builds an input form from each process description's
JSON Schema (plain controls for scalars, a JSON editor with *Skeleton*/*Example*
buttons for structured inputs), lets you pick the requested outputs and the `Prefer`
mode, and shows the raw request and response with the OGC-relevant headers
(`Location`, `Preference-Applied`, `Link`, `OGC-Output-Values-Count`). Async
executions are polled until the job finishes, and the Jobs tab filters the job list,
fetches results and dismisses jobs. An `X-Api-Key` typed in the header is sent with
every request (kept in `sessionStorage` only).

The **Recipe** tab is driven by a CWL `Workflow` document - see [The recipe as a
CWL workflow](#the-recipe-as-a-cwl-workflow). The flow diagram is generated from
that document rather than hard-coded, so editing `steps` changes the diagram, the
order and what runs. Tasks light up as they execute, show their own timing, and
can be clicked to inspect exactly what they sent and received. **Run as** picks
between one request per step and a single Part 3 nested request, and **Compare**
runs `plot-bag-matching-pdok` on the same plot and diffs the two `match_result`
documents.

A Base URL typed without a scheme gets `https://` (or `http://` for loopback).
Without that, the browser treats the value as a relative URL and resolves it
against the page itself, which from a `file://` page fails as a bare network
error that looks like the server being down.

The page calls the API cross-origin, so it needs `CORS_ALLOWED_ORIGINS` to cover it —
the default `*` already does; a `file://` page sends `Origin: null`.

---

## Configuration

The server is configured entirely through environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Port the HTTP server listens on |
| `BASE_URL` | `http://localhost:<PORT>` | Public base URL included in API links |
| `JOB_MAX_AGE_HOURS` | `48` | Hours since last access before a job is evicted from memory |
| `CORS_ALLOWED_ORIGINS` | `*` | Comma-separated origins allowed to call the API from a browser; `*` allows any, `null` allows pages opened from `file://` |
| `PDOK_BAG_WFS_URL` | `https://service.pdok.nl/lv/bag/wfs/v2_0` | BAG WFS queried by `plot-bag-matching-pdok` — point it at a mirror or a test double |

Set these in your shell, `.env` file, or the `environment` section of `docker-compose.yml`.

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Landing page with links to all resources |
| `GET` | `/api` | OpenAPI 3.0 definition |
| `GET` | `/conformance` | Declared OGC conformance classes |
| `GET` | `/processes` | List processes (`?limit=` 1–10000, default 10, with `next`/`prev` links) |
| `GET` | `/processes/{id}` | Full description of a single process |
| `POST` | `/processes/{id}/execution` | Submit a process for execution |
| `GET` | `/jobs` | List jobs. Filters: `processID`, `status`, `type`, `datetime`, `minDuration`, `maxDuration`, `limit` |
| `GET` | `/jobs/{jobId}` | Status of a specific job |
| `GET` | `/jobs/{jobId}/results` | Results document of a completed job (`?outputs=a,b` to select) |
| `GET` | `/jobs/{jobId}/results/{outputId}` | Raw value of one output, with an `OGC-Output-Values-Count` header (also `/{outputId}/0`) |
| `DELETE` | `/jobs/{jobId}` | Dismiss a job (finished or dismissed jobs are removed) |

Responses are `application/json`. Errors are RFC 9457 `application/problem+json` with OGC exception types where the spec defines one (`no-such-process`, `no-such-job`, `result-not-ready`, …).

---

## Execution Modes

A process can be executed synchronously (result returned immediately) or asynchronously (job created, polled separately). Control this with the `Prefer` HTTP header:

| Header value | Behaviour |
|---|---|
| `Prefer: respond-sync` | Synchronous — result in the response body |
| `Prefer: respond-async` | Asynchronous — `201 Created` with a job status and `Location` header |
| *(absent)* | Synchronous if the process supports it, otherwise asynchronous |

An honoured preference is echoed in `Preference-Applied`. If the process doesn't support the requested mode, the server uses the mode it does support and omits `Preference-Applied`. `Prefer` may list several preferences (`return=representation, respond-async`); unknown ones are ignored.

With a `subscriber`, the server POSTs the job status to `inProgressUri` and `failedUri`, and the results document to `successUri` (30 s timeout, best effort).

Synchronous response shape, based on the execute request's `outputs` member (absent = all outputs):

| Requested outputs | Response |
|---|---|
| none (`"outputs": {}`) | `204 No Content` |
| one | `200` with the raw output value |
| several | `200` JSON results document `{"<outputId>": {"value": …}}` plus `Link: <https://www.opengis.net/def/profile/OGC/0/ogc-results>; rel="profile"` |

Inputs may be bare values or qualified values (`{"value": …, "mediaType": …}`).

---

## Available Processes

### `plot-bag-matching`

Matches a housing-plan plot (kavel) against BAG buildings and returns the matched BAG IDs with an overlap score. Plots without a full match are flagged. Ported from the `test_Process_plot_bag_matching.ipynb` notebook. It makes no upstream calls: the caller supplies the BAG features for the search area.

**Inputs**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `plot` | object | yes | `plan_dataid`, `geometry` (WGS84 GeoJSON Polygon/MultiPolygon), optional `plan_plannaam`, `gemeente`, `planned_bruto`, `planned_netto`, `planologische_status` |
| `bag_buildings` | array | no | BAG panden/verblijfsobjecten as WGS84 GeoJSON Features with `properties.identificatie` and `properties.bouwjaar` |
| `min_overlap_ratio` | number | no | Minimum fraction of a building inside the plot to count as a candidate (default `0.05`) |
| `full_match_threshold` | number | no | Overlap ratio from which a building is a full match (default `0.95`) |

**Output:** `match_result` containing `plan_dataid`, `plan_plannaam`, `matches` (`bag_id`, `bouwjaar`, `overlap_ratio`, `score`, `match_type`: `full`/`partial`, sorted by overlap) and `summary` (`n_candidates`, `n_full_matches`, `n_partial_matches`, `fully_matched`, `flagged`, `flag_reason`: `geen_bag_object_binnen_kavel` / `alleen_partiele_match` / `null`).

Overlap is the share of a building's area inside the plot, computed in RD New (EPSG:28992). Self-intersecting plot polygons are repaired by splitting them at their self-intersections. For a "bowtie" this keeps both lobes, where the notebook's shapely `buffer(0)` kept one. Buildings without an ID or with unreadable geometry are skipped and logged. Invalid input returns `400`.

```bash
curl -X POST http://localhost:8080/processes/plot-bag-matching/execution \
  -H "Content-Type: application/json" -H "Prefer: respond-sync" \
  -d '{"inputs": {
        "plot": {"plan_dataid": "example-1", "geometry": {"type": "Polygon",
          "coordinates": [[[5.0,52.0],[5.002,52.0],[5.002,52.002],[5.0,52.002],[5.0,52.0]]]}},
        "bag_buildings": [{"type": "Feature",
          "properties": {"identificatie": "NL.IMBAG.PAND.0001", "bouwjaar": 2019},
          "geometry": {"type": "Polygon",
            "coordinates": [[[5.0005,52.0005],[5.0008,52.0005],[5.0008,52.0008],[5.0005,52.0008],[5.0005,52.0005]]]}}]}}'
```

---

### `plot-bag-matching-pdok`

The same matching as `plot-bag-matching`, with the BAG features fetched by the process instead of supplied by the caller: it queries the panden intersecting the plot's bounding box from the public [PDOK BAG WFS](https://service.pdok.nl/lv/bag/wfs/v2_0) (no API key). Every building that overlaps the plot also overlaps the plot's bounding box, so the bbox query loses no candidate.

Only `bag:pand` is queried. PDOK serves `verblijfsobject`, `ligplaats` and `standplaats` as points, and the overlap ratio is a share of a building's *area*, so points can never match.

**Inputs**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `plot` | object | yes | Same as `plot-bag-matching` |
| `min_overlap_ratio` | number | no | Minimum fraction of a building inside the plot to count as a candidate (default `0.05`) |
| `full_match_threshold` | number | no | Overlap ratio from which a building is a full match (default `0.95`) |

**Outputs:** `match_result` (identical to `plot-bag-matching`) and `bag_query` — `source_url`, `collection`, `bbox` (the queried extent in WGS84), `feature_count`, `requests` and `truncated`. `bag_query` is what tells an empty match result ("no BAG objects inside this plot") apart from an empty BAG response.

Because this process declares two outputs, a synchronous execution returns a results document rather than a raw value. Add `"outputs": {"match_result": {}}` to get the match result on its own.

Results are paged out of the WFS 1000 features at a time and capped at 20 000 per execution; a plot large enough to hit the cap comes back with `"truncated": true`. A failure of the PDOK service is reported as `502`, invalid input as `400`.

```bash
curl -X POST http://localhost:8080/processes/plot-bag-matching-pdok/execution \
  -H "Content-Type: application/json" -H "Prefer: respond-sync" \
  -d '{"inputs": {"plot": {"plan_dataid": "amsterdam-test", "geometry": {"type": "Polygon",
        "coordinates": [[[4.8895,52.3705],[4.8905,52.3705],[4.8905,52.3715],[4.8895,52.3715],[4.8895,52.3705]]]}}}}'
```

---

### Plot→BAG matching as separate steps

`plot-bag-matching-pdok` is also available as its five stages, each executable on
its own, so a recipe can chain them, inspect every intermediate value and replace
one stage without touching the others.

| Step | Process | Inputs | Outputs |
|------|---------|--------|---------|
| 1 | `plot-geometry-prepare` | `geometry` (or `plot`) | `geometry`, `geometry_report` |
| 2 | `plot-bbox` | `geometry` | `bbox` |
| 3 | `bag-panden-fetch` | `bbox` | `bag_buildings`, `bag_query` |
| 4 | `plot-bag-overlap` | `geometry`, `bag_buildings`, `min_overlap_ratio` | `overlaps`, `skipped` |
| 5 | `bag-match-classify` | `overlaps`, `plan_dataid`, `plan_plannaam`, `full_match_threshold` | `match_result` |

```
            ┌─ 2 plot-bbox ─ 3 bag-panden-fetch ─┐
1 plot-geometry-prepare ─┤                                    ├─ 4 plot-bag-overlap ─ 5 bag-match-classify
            └─ bag_buildings supplied ───────────┘
```

Chaining all five reproduces `plot-bag-matching-pdok` exactly; skipping steps 2
and 3 and supplying `bag_buildings` reproduces `plot-bag-matching`. Both routes
share one implementation ([ogc-api/processes/plotbag/steps.go](ogc-api/processes/plotbag/steps.go)),
and `TestSteps_ChainMatchesMonolith` asserts that the chained output is identical
to the single call, so they cannot drift apart.

Two details make the split exact rather than approximate:

- **`overlap_ratio` is unrounded in step 4.** `bag-match-classify` compares it
  against `full_match_threshold` and only then rounds to 4 decimals, which is the
  order `plot-bag-matching` uses internally. Rounding in step 4 would move
  buildings across the threshold at the fourth decimal.
- **Step 1 validates and repairs in lon/lat**, not RD New, because its output has
  to be GeoJSON and `rd.go` transforms lon/lat to RD New and not back. For valid
  geometry both routes agree exactly. For a self-intersecting polygon the repaired
  lobes can differ marginally, since the projection is not perfectly affine. Areas
  and overlaps are measured in RD New either way.

Splitting step 4 from step 5 is what makes a threshold sweep cheap: the overlaps
are computed once and `bag-match-classify` can be re-run at any
`full_match_threshold` without refetching from PDOK or recomputing any geometry.
Splitting step 3 out means the BAG features can be fetched once and reused across
several plots in the same area.

```bash
# Step 1 -> prepared geometry and a report on what had to be repaired
curl -X POST http://localhost:8080/processes/plot-geometry-prepare/execution \
  -H 'Content-Type: application/json' \
  -d '{"inputs":{"geometry":{"type":"Polygon","coordinates":[[[5.1214,52.0907],[5.1230,52.0907],[5.1230,52.0918],[5.1214,52.0918],[5.1214,52.0907]]]}},
       "outputs":{"geometry_report":{}}}'
```

When several outputs are requested the response is a results document and each
entry is an OGC qualified value — `{"value": …}` — so a recipe has to unwrap
`.value` before feeding an output into the next step. Requesting a single output
returns the raw value instead.

### Chaining the steps in one request (Part 3)

With the `nested-processes` class, an input value may itself be an execution
request: an object carrying a `process` key. The server runs that process and
substitutes its output, so the whole recipe becomes a single POST and the
intermediate values never leave the server.

A bare process ID is a relative reference resolved against this API, so
`{"process": "plot-bbox"}` means `{root}/processes/plot-bbox`. An absolute URI
pointing back at this server also works; one pointing elsewhere is rejected,
because executing remote processes is the separate `remote-core-processes` class.

```bash
curl -X POST http://localhost:8080/processes/bag-match-classify/execution \
  -H 'Content-Type: application/json' \
  -d '{"inputs":{
        "plan_dataid":"utrecht-demo",
        "overlaps":{
          "process":"plot-bag-overlap",
          "inputs":{
            "geometry":{"process":"plot-geometry-prepare",
                        "inputs":{"geometry":{"type":"Polygon","coordinates":[[[5.1214,52.0907],[5.1230,52.0907],[5.1230,52.0918],[5.1214,52.0918],[5.1214,52.0907]]]}},
                        "outputs":{"geometry":{}}},
            "bag_buildings":{"process":"bag-panden-fetch",
                             "inputs":{"bbox":{"process":"plot-bbox","inputs":{"geometry":{"type":"Polygon","coordinates":[[[5.1214,52.0907],[5.1230,52.0907],[5.1230,52.0918],[5.1214,52.0918],[5.1214,52.0907]]]}}}},
                             "outputs":{"bag_buildings":{}}}
          },
          "outputs":{"overlaps":{}}
        }
      },"outputs":{"match_result":{}}}'
```

Rules this server applies:

- **A nested process must resolve to one output**, because it stands where a
  single value is expected. Name it by requesting exactly one output; a process
  with only one output needs no selection. Anything else is a 400 that lists the
  available outputs.
- **Nested outputs cross as JSON.** A process function returns Go-native values
  (`plot-bbox` returns a `[4]float64`) while every input parser expects what
  `encoding/json` produces (`[]any`). Over HTTP that conversion is free; resolving
  in process, it is done explicitly, so a chained request behaves exactly like a
  chained set of requests.
- **A nested failure keeps its own status.** A PDOK outage inside step 3 of a
  chained request is still a 502, not a 400, and the message names the process
  that failed.
- **Nesting is bounded** at 8 deep. Processes referencing each other in a cycle
  surface as a depth error rather than a hang.
- **The top-level `process` key is ignored**, as the standard requires: the
  request URI already identifies the process.

> **Nesting is a tree, not a DAG.** `plot-geometry-prepare` feeds both the bbox
> branch and the overlap step, so the nested encoding contains it twice and the
> server runs it twice. Running the steps one at a time honours the DAG and runs
> it once. Same answer either way - verified against `plot-bag-matching-pdok` -
> but the nested form does more work when a step has several consumers.

### The recipe as a CWL workflow

The console's Recipe tab is driven by a [Common Workflow
Language](https://www.commonwl.org/) `Workflow` document in its JSON encoding.
The step order, the diagram and the execution all come from the document, so
editing `steps` changes what runs - nothing about the five steps is hard-coded.

CWL is the workflow definition only. These steps are OGC processes, not CWL
`CommandLineTool`s, so `run` names a process ID, written as `ogc:<id>`, `#<id>`
or a bare `<id>`.

```json
{
  "cwlVersion": "v1.2",
  "class": "Workflow",
  "inputs": { "geometry": "Any", "plan_dataid": "string" },
  "outputs": { "match_result": { "type": "Any", "outputSource": "classify/match_result" } },
  "steps": {
    "prepare": { "run": "ogc:plot-geometry-prepare", "in": { "geometry": "geometry" },
                 "out": ["geometry", "geometry_report"] },
    "bbox":    { "run": "ogc:plot-bbox", "in": { "geometry": "prepare/geometry" }, "out": ["bbox"] },
    "fetch":   { "run": "ogc:bag-panden-fetch", "in": { "bbox": "bbox/bbox" },
                 "out": ["bag_buildings", "bag_query"] },
    "overlap": { "run": "ogc:plot-bag-overlap",
                 "in": { "geometry": "prepare/geometry", "bag_buildings": "fetch/bag_buildings" },
                 "out": ["overlaps", "skipped"] },
    "classify":{ "run": "ogc:bag-match-classify", "in": { "overlaps": "overlap/overlaps",
                 "plan_dataid": "plan_dataid" }, "out": ["match_result"] }
  }
}
```

The console supports `in` bindings written as a bare source string, as
`{"source": …}` or as `{"default": …}` for a literal. `valueFrom` is rejected
rather than ignored, since silently dropping an expression would change what the
workflow means. Steps are ordered by a topological sort of the `in`/`out` graph,
and a cycle is reported instead of executed.

The same document runs two ways:

| Mode | What happens | Trade-off |
|------|--------------|-----------|
| Step by step | One execution request per step, driven from the browser | Every intermediate value is visible; the DAG is honoured, so a shared step runs once |
| One nested request | Compiled into a Part 3 nested execution request | One round trip, intermediates stay server-side; a shared step is duplicated and runs twice |

Both were verified to return the same `match_result` as
`plot-bag-matching-pdok` against live PDOK data.

## Adding a New Process

All processes are registered in [ogc-api/handlers/functions.go](ogc-api/handlers/functions.go). To add one:

1. Write a `ProcessFunc` implementation:

```go
func myProcess(inputs map[string]any) (map[string]any, error) {
    value, _ := inputs["my_input"].(string)
    if value == "" {
        return nil, &ProcessError{Status: http.StatusBadRequest, Msg: "my_input is empty"}
    }
    return map[string]any{"result": value}, nil
}
```

Return a `*ProcessError` to choose the HTTP status of a failure (`upstreamError(msg)` gives 502); any other error is reported as 500. Processes with their own helpers belong in a package under `ogc-api/processes/`.

2. Add a `ProcessDefinition` to the `processRegistry` slice:

```go
{
    Description: models.ProcessDescription{
        ProcessSummary: models.ProcessSummary{
            ID:                "my-process",
            Title:             "My Process",
            Description:       "Does something useful.",
            Version:           "1.0.0",
            JobControlOptions: []string{"sync-execute", "async-execute"},
        },
        Inputs: map[string]models.InputDescription{
            "my_input": {
                Title:     "My Input",
                Schema:    map[string]string{"type": "string"},
                MinOccurs: 1,
            },
        },
        Outputs: map[string]models.OutputDescription{
            "result": {
                Title:  "Result",
                Schema: map[string]string{"type": "string"},
            },
        },
    },
    Fn: myProcess,
},
```

The process will be automatically discovered and registered at startup.

---

## Development

```bash
# Run tests
make test

# Run tests with verbose output
make test-v

# Static analysis
make vet

# Format all Go files
make fmt
```

### API Key in CI/CD

Tests that exercise the execution endpoint pass the `API_KEY` environment variable as the `X-Api-Key` header. Set this as a repository/pipeline secret in your CI/CD system (GitHub Actions, GitLab CI, etc.):

**GitHub Actions example**

```yaml
- name: Run tests
  run: make test
  env:
    API_KEY: ${{ secrets.API_KEY }}
```

If `API_KEY` is not set (e.g. local development), the tests fall back to a placeholder value.

---

## Design Notes

- **No framework** — uses only the Go standard library `net/http` router.
- **In-memory store** — processes and jobs are held in a thread-safe in-memory store (`store/store.go`). Data is lost on restart; replace `store.Store` with a persistent backend for production use.
- **Job TTL** — a background goroutine runs every hour and evicts jobs that have not been accessed within `JOB_MAX_AGE_HOURS` (default 48 h). Access is tracked on every status, result, or dismiss request.
- **Structured logging** — uses `log/slog` with JSON output for easy integration with log aggregators.
- **Job IDs** — UUIDs prefixed with `job-` (e.g. `job-3f2a1b4c-…`).
- **Async execution** — jobs run in a Go goroutine; no queue or worker pool is used, suitable for lightweight processes.

---

## License

[EUPL-1.2](LICENSE)
