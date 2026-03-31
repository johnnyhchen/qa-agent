package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpcHealth "google.golang.org/grpc/health/grpc_health_v1"

	"qa-agent/internal/model"
	"qa-agent/internal/runner"
	"qa-agent/internal/sandbox"
)

type TaskSpec struct {
	HTTPRequests []HTTPRequest `json:"http_requests"`
	GRPCRequests []GRPCRequest `json:"grpc_requests,omitempty"`
}

type HTTPRequest struct {
	Method             string            `json:"method"`
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers,omitempty"`
	Body               string            `json:"body,omitempty"`
	ExpectStatus       int               `json:"expect_status"`
	ExpectBodyContains string            `json:"expect_body_contains,omitempty"`
}

type GRPCRequest struct {
	Address      string `json:"address"`
	Service      string `json:"service"`
	ExpectStatus string `json:"expect_status"`
}

type transcriptEntry struct {
	Protocol   string         `json:"protocol"`
	StartedAt  string         `json:"started_at"`
	DurationMS int64          `json:"duration_ms"`
	Request    map[string]any `json:"request"`
	Response   map[string]any `json:"response"`
	RetryCount int            `json:"retry_count"`
	Error      string         `json:"error,omitempty"`
	Redacted   []string       `json:"redacted,omitempty"`
}

type Adapter struct {
	client      *http.Client
	dialTimeout time.Duration
}

func NewAdapter(timeout time.Duration) *Adapter {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Adapter{
		client: &http.Client{
			Timeout: timeout,
		},
		dialTimeout: timeout,
	}
}

func (a *Adapter) Run(ctx context.Context, task model.Task, _ sandbox.Sandbox, artifactDir string) (runner.Result, error) {
	spec, err := ParseTaskSpec(task)
	if err != nil {
		return runner.Result{}, err
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return runner.Result{}, err
	}

	var entries []transcriptEntry
	var failed int

	for _, request := range spec.HTTPRequests {
		entry, ok, err := a.executeHTTP(ctx, request)
		if err != nil {
			return runner.Result{}, err
		}
		if !ok {
			failed++
		}
		entries = append(entries, entry)
	}

	for _, request := range spec.GRPCRequests {
		entry, ok, err := a.executeGRPC(ctx, request)
		if err != nil {
			return runner.Result{}, err
		}
		if !ok {
			failed++
		}
		entries = append(entries, entry)
	}

	transcriptPath := filepath.Join(artifactDir, "api-transcript.json")
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return runner.Result{}, err
	}
	if err := os.WriteFile(transcriptPath, raw, 0o644); err != nil {
		return runner.Result{}, err
	}

	outcome := model.RunOutcomePass
	summary := fmt.Sprintf("executed %d requests with 0 failures", len(entries))
	if failed > 0 {
		outcome = model.RunOutcomeFail
		summary = fmt.Sprintf("executed %d requests with %d failures", len(entries), failed)
	}

	return runner.Result{
		Outcome:       outcome,
		Summary:       summary,
		EvidenceFiles: []string{transcriptPath},
		StabilityHints: []string{
			"deterministic_api_runner",
		},
	}, nil
}

func (a *Adapter) executeHTTP(ctx context.Context, request HTTPRequest) (transcriptEntry, bool, error) {
	started := time.Now().UTC()
	method := strings.TrimSpace(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		return transcriptEntry{}, false, err
	}
	for key, value := range request.Headers {
		req.Header.Set(key, value)
	}
	// Default Content-Type to application/json for mutation methods
	// when the caller hasn't set it explicitly. Many APIs (Axum, Express)
	// reject requests without this header even when the body is empty.
	if req.Header.Get("Content-Type") == "" {
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			req.Header.Set("Content-Type", "application/json")
		}
	}

	response, err := a.client.Do(req)
	entry := transcriptEntry{
		Protocol:   "http",
		StartedAt:  started.Format(time.RFC3339Nano),
		RetryCount: 0,
		Request: map[string]any{
			"method":  method,
			"url":     request.URL,
			"headers": redactHeaders(request.Headers),
			"body":    request.Body,
		},
		Response: map[string]any{},
		Redacted: []string{"authorization", "cookie", "set-cookie"},
	}

	if err != nil {
		entry.DurationMS = time.Since(started).Milliseconds()
		entry.Error = err.Error()
		return entry, false, nil
	}
	defer response.Body.Close()

	bodyBytes, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if readErr != nil {
		return transcriptEntry{}, false, readErr
	}

	entry.DurationMS = time.Since(started).Milliseconds()
	entry.Response["status"] = response.StatusCode
	entry.Response["headers"] = redactHeaders(flattenHeaders(response.Header))
	entry.Response["body"] = string(bodyBytes)

	ok := response.StatusCode == request.ExpectStatus
	if request.ExpectBodyContains != "" && !strings.Contains(string(bodyBytes), request.ExpectBodyContains) {
		ok = false
	}
	if request.ExpectStatus == 0 {
		ok = response.StatusCode >= 200 && response.StatusCode < 300
	}
	return entry, ok, nil
}

func (a *Adapter) executeGRPC(ctx context.Context, request GRPCRequest) (transcriptEntry, bool, error) {
	started := time.Now().UTC()
	dialCtx, cancel := context.WithTimeout(ctx, a.dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, request.Address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	entry := transcriptEntry{
		Protocol:   "grpc",
		StartedAt:  started.Format(time.RFC3339Nano),
		RetryCount: 0,
		Request: map[string]any{
			"address": request.Address,
			"service": request.Service,
			"method":  "Check",
		},
		Response: map[string]any{},
	}
	if err != nil {
		entry.DurationMS = time.Since(started).Milliseconds()
		entry.Error = err.Error()
		return entry, false, nil
	}
	defer conn.Close()

	client := grpcHealth.NewHealthClient(conn)
	resp, err := client.Check(ctx, &grpcHealth.HealthCheckRequest{Service: request.Service})
	entry.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		entry.Error = err.Error()
		return entry, false, nil
	}

	status := resp.GetStatus().String()
	entry.Response["status"] = status
	ok := status == request.ExpectStatus
	if strings.TrimSpace(request.ExpectStatus) == "" {
		ok = status == grpcHealth.HealthCheckResponse_SERVING.String()
	}
	return entry, ok, nil
}

func ParseTaskSpec(task model.Task) (TaskSpec, error) {
	if task.Payload == nil {
		return TaskSpec{}, errors.New("api task payload is required")
	}

	spec := TaskSpec{}
	if rawHTTP, ok := task.Payload["http_requests"].([]any); ok {
		for _, item := range rawHTTP {
			requestMap, ok := item.(map[string]any)
			if !ok {
				return TaskSpec{}, errors.New("api payload.http_requests must contain objects")
			}
			request := HTTPRequest{
				Method: strings.TrimSpace(getString(requestMap, "method")),
				URL:    strings.TrimSpace(getString(requestMap, "url")),
				Body:   getString(requestMap, "body"),
			}
			if request.Method == "" {
				request.Method = http.MethodGet
			}
			if request.URL == "" {
				return TaskSpec{}, errors.New("api payload.http_requests.url is required")
			}
			if value, ok := requestMap["expect_status"].(float64); ok {
				request.ExpectStatus = int(value)
			}
			request.ExpectBodyContains = getString(requestMap, "expect_body_contains")
			request.Headers = getStringMap(requestMap["headers"])
			spec.HTTPRequests = append(spec.HTTPRequests, request)
		}
	}

	if rawGRPC, ok := task.Payload["grpc_requests"].([]any); ok {
		for _, item := range rawGRPC {
			requestMap, ok := item.(map[string]any)
			if !ok {
				return TaskSpec{}, errors.New("api payload.grpc_requests must contain objects")
			}
			request := GRPCRequest{
				Address:      strings.TrimSpace(getString(requestMap, "address")),
				Service:      strings.TrimSpace(getString(requestMap, "service")),
				ExpectStatus: strings.TrimSpace(getString(requestMap, "expect_status")),
			}
			if request.Address == "" {
				return TaskSpec{}, errors.New("api payload.grpc_requests.address is required")
			}
			spec.GRPCRequests = append(spec.GRPCRequests, request)
		}
	}

	if len(spec.HTTPRequests) == 0 && len(spec.GRPCRequests) == 0 {
		return TaskSpec{}, errors.New("api task must include http_requests or grpc_requests")
	}
	return spec, nil
}

func getString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func getStringMap(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(raw))
	for key, item := range raw {
		text, ok := item.(string)
		if ok {
			result[key] = text
		}
	}
	return result
}

func flattenHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		result[key] = strings.Join(values, ",")
	}
	return result
}

func redactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "cookie" || lower == "set-cookie" || strings.Contains(lower, "token") {
			result[key] = "[redacted]"
			continue
		}
		result[key] = value
	}
	return result
}
