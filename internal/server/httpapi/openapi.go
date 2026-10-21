package httpapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed openapi.json
var openAPISpec []byte

func (a *API) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if len(openAPISpec) > 0 {
		_, _ = w.Write(openAPISpec)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"openapi": "3.0.3", "info": map[string]string{"title": "Kairos API", "version": "1.18.0"}})
}
