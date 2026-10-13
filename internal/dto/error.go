// Package dto holds HTTP request and response shapes.
package dto

// ErrorResponse is the body of every non-2xx JSON response.
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}
