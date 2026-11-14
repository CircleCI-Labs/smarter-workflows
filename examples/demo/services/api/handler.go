package main

import (
	"encoding/json"
	"net/http"
)

// Greeting is the response of GET /greeting. Its fields follow
// libs/proto/schema.json.
type Greeting struct {
	Message string `json:"message"`
	Name    string `json:"name"`
}

// newMux returns the routes of the api.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /greeting", greeting)
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func greeting(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "world"
	}
	writeJSON(w, http.StatusOK, Greeting{Message: "Hello, " + name + "!", Name: name})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
