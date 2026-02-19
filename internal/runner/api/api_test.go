package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpcHealth "google.golang.org/grpc/health/grpc_health_v1"

	"qa-agent/internal/model"
	"qa-agent/internal/sandbox"
)

func TestAdapterHTTPIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Authorization", "secret")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	adapter := NewAdapter(0)
	artifactDir := t.TempDir()

	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_http",
		RunID:         "run_1",
		Payload: map[string]any{
			"http_requests": []any{
				map[string]any{
					"method":               "GET",
					"url":                  server.URL,
					"expect_status":        float64(200),
					"expect_body_contains": `"ok":true`,
					"headers": map[string]any{
						"Authorization": "Bearer token",
					},
				},
			},
		},
	}

	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{}, artifactDir)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomePass {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomePass)
	}
	if len(result.EvidenceFiles) != 1 {
		t.Fatalf("len(result.EvidenceFiles) = %d, want 1", len(result.EvidenceFiles))
	}
	if !strings.HasSuffix(result.EvidenceFiles[0], "api-transcript.json") {
		t.Fatalf("unexpected evidence file path: %s", result.EvidenceFiles[0])
	}
}

func TestAdapterGRPCHealthIntegration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("qa.agent", grpcHealth.HealthCheckResponse_SERVING)
	grpcHealth.RegisterHealthServer(grpcServer, healthServer)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	adapter := NewAdapter(0)
	artifactDir := t.TempDir()

	task := model.Task{
		SchemaVersion: model.CurrentSchemaVersion,
		TaskID:        "task_grpc",
		RunID:         "run_1",
		Payload: map[string]any{
			"grpc_requests": []any{
				map[string]any{
					"address":       listener.Addr().String(),
					"service":       "qa.agent",
					"expect_status": "SERVING",
				},
			},
		},
	}

	result, err := adapter.Run(context.Background(), task, sandbox.Sandbox{}, artifactDir)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != model.RunOutcomePass {
		t.Fatalf("result.Outcome = %s, want %s", result.Outcome, model.RunOutcomePass)
	}
	if len(result.EvidenceFiles) != 1 {
		t.Fatalf("len(result.EvidenceFiles) = %d, want 1", len(result.EvidenceFiles))
	}
	if filepath.Base(result.EvidenceFiles[0]) != "api-transcript.json" {
		t.Fatalf("unexpected transcript path: %s", result.EvidenceFiles[0])
	}
}
