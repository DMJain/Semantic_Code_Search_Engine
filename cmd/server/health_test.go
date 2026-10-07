package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		ollamaErr  error
		wantStatus string
	}{
		{name: "all ok", wantStatus: "ok"},
		{name: "ollama down", ollamaErr: errors.New("connection refused"), wantStatus: "degraded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := healthHandler(map[string]checkFunc{
				"database": func(context.Context) error { return nil },
				"ollama":   func(context.Context) error { return tt.ollamaErr },
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody))

			if rec.Code != http.StatusOK {
				t.Fatalf("status code = %d, want 200", rec.Code)
			}
			var body healthResponse
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", body.Status, tt.wantStatus)
			}
			if !body.Checks["database"].OK {
				t.Errorf("database check = %+v, want ok", body.Checks["database"])
			}
			if got := body.Checks["ollama"]; got.OK != (tt.ollamaErr == nil) {
				t.Errorf("ollama check = %+v", got)
			}
		})
	}
}
