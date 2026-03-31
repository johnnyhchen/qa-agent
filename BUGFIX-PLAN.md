# QA-Agent Planner Bugfix Plan

## Context

During validation of the MS Teams digital twin (78 routes), two bugs in the
planner prevented the qa-agent from correctly testing endpoints:

1. **Period-splitting fragments URLs** — `splitCriteria` uses
   `strings.Split(description, ".")` which breaks URLs containing dots
   (e.g., `/api/users.create` → splits into `/api/users` and `create ...`).
   A feature description with 80 intended criteria produced 120 fragments.

2. **Request bodies are never sent** — `extractHTTPRequests` extracts method,
   URL, expected status, and body-contains assertion, but never extracts the
   JSON request body from the criterion text. All POST/PUT tasks execute with
   empty bodies, causing server-side 400 errors on endpoints that require input.

## Fix 1: URL-aware criterion splitting

**File:** `internal/agents/planner/planner.go`

**Problem:** `strings.Split(description, ".")` splits on every period, including
those inside URLs (`http://host/api/users.create`) and version paths (`/v1.0/`).

**Fix:** Replace naive split with a smarter splitter that skips periods inside
URLs. The approach:
- Use a regex to find all URL spans in the text first
- Split on `.` only when the period is NOT inside a URL span
- A period is a criterion boundary only if it is followed by whitespace and an
  uppercase letter (sentence boundary), or is at the end of the string

**Implementation:** Replace `splitCriteria` with a version that uses a
sentence-boundary regex: split on `. ` (period-space) followed by an uppercase
letter, rather than splitting on every `.`.

## Fix 2: Extract JSON request bodies from criterion text

**File:** `internal/agents/planner/planner.go`

**Problem:** `extractHTTPRequests` builds the payload with method, URL, and
status but ignores `with body {...}` patterns in the criterion text.

**Fix:** Add a `bodyPattern` regex that captures JSON objects following
`with body` or `body` phrases. Extract the JSON string and include it as the
`body` field in the HTTP request payload.

**Pattern:** `(?i)with\s+body\s+(\{[^}]*\}|\[[^\]]*\])` — matches
`with body {"key":"value"}` or `with body [...]`.

**Implementation:** After extracting method/URL, scan for the body pattern in
the text surrounding the HTTP request match and add it to the request map.

## Test Plan

Add tests in `internal/agents/planner/planner_payload_test.go`:

1. `TestSplitCriteria_PreservesDottedURLs` — verify URLs like
   `/api/users.create` and `/v1.0/users` don't cause extra splits
2. `TestExtractHTTPRequests_WithBody` — verify `with body {"key":"val"}`
   is extracted into the payload
3. `TestExtractHTTPRequests_WithBodyNested` — verify nested JSON bodies work
4. `TestPlanner_POSTWithBody_HasPayloadBody` — end-to-end: a POST criterion
   with a body produces a task payload containing the body string

Run existing tests to confirm no regressions:
```
go test ./internal/agents/planner/... -count=1 -shuffle=on
go test ./internal/runner/api/... -count=1 -shuffle=on
```
