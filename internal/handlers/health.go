package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/mubeendevelops/convoy-chat/internal/store"
)

// Health reports the reachability of Postgres and Redis. It returns 200 when
// both are reachable and 503 otherwise, so it can back a load balancer or
// orchestrator liveness/readiness check.
func Health(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resp := make(map[string]string, 2)
		healthy := true

		if err := s.PingPostgres(ctx); err != nil {
			resp["postgres"] = "error: " + err.Error()
			healthy = false
		} else {
			resp["postgres"] = "ok"
		}

		if err := s.PingRedis(ctx); err != nil {
			resp["redis"] = "error: " + err.Error()
			healthy = false
		} else {
			resp["redis"] = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(resp)
	}
}
