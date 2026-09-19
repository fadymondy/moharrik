package server

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// apiError is the one error shape the API speaks: {"error": "...", "details": [...]}.
// Huma's default is RFC 7807 (title/detail); replacing huma.NewError makes the
// generated handlers, the hand-written ones and the guard all answer alike.
type apiError struct {
	status  int
	Message string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func (e *apiError) Error() string  { return e.Message }
func (e *apiError) GetStatus() int { return e.status }

func installErrors() {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		e := &apiError{status: status, Message: msg}
		for _, err := range errs {
			if err != nil && status < 500 {
				e.Details = append(e.Details, err.Error())
			}
		}
		return e
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
