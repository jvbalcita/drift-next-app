package service

import "net/http"

var localOrigins = map[string]struct{}{
	"http://localhost:5173":  {},
	"http://127.0.0.1:5173":  {},
	"tauri://localhost":      {},
	"http://tauri.localhost": {},
}

func localCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if _, allowed := localOrigins[origin]; allowed {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Vary", "Origin")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Drift-Lab-Token")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if request.Method == http.MethodOptions {
			if origin == "" {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			if _, allowed := localOrigins[origin]; !allowed {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
