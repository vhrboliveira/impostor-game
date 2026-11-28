package httpapi

import (
	"encoding/json"
	"net/http"
)

type statusBody struct {
	Status string `json:"status"`
}

// errorBody is the shape of every error response. Code is a stable English identifier
// (for example "room_not_found"); the web app translates it into Portuguese.
type errorBody struct {
	Code string `json:"code"`
}

// writeJSON writes a JSON response. It is never cached (the create and join responses carry the
// session token) nor sniffed as another type.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorBody{Code: code})
}
