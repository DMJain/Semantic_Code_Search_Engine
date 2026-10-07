package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const healthCheckTimeout = 2 * time.Second

type checkFunc func(ctx context.Context) error

type checkStatus struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type healthResponse struct {
	// Status is "ok" when every check passes, otherwise "degraded".
	Status string                 `json:"status"`
	Checks map[string]checkStatus `json:"checks"`
}

// healthHandler runs every check concurrently and always answers 200: the
// process is alive, and the body says which dependencies are down.
func healthHandler(checks map[string]checkFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		resp := healthResponse{Status: "ok", Checks: make(map[string]checkStatus, len(checks))}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, check := range checks {
			wg.Go(func() {
				st := checkStatus{OK: true}
				if err := check(ctx); err != nil {
					st = checkStatus{OK: false, Error: err.Error()}
				}
				mu.Lock()
				defer mu.Unlock()
				resp.Checks[name] = st
				if !st.OK {
					resp.Status = "degraded"
				}
			})
		}
		wg.Wait()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Warn("write healthz response", "err", err)
		}
	})
}
