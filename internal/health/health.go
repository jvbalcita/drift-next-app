package health

import (
	"encoding/json"
	"net/http"
	"time"
)

type response struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	At      string `json:"at"`
}

// Handler exposes intentionally small liveness/readiness endpoints. Readiness is
// supplied by the caller so dependency checks can be added without changing the
// HTTP contract.
func Handler(service string, ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, response{Service: service, Status: "ok", At: now()})
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if ready == nil || !ready() {
			writeJSON(writer, http.StatusServiceUnavailable, response{Service: service, Status: "not_ready", At: now()})
			return
		}
		writeJSON(writer, http.StatusOK, response{Service: service, Status: "ready", At: now()})
	})
	return mux
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func writeJSON(writer http.ResponseWriter, status int, value response) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
