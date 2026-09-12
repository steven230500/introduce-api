// Package httpx holds the small helpers every handler shares: JSON encoding,
// request decoding, and one consistent error shape.
package httpx

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

// Error is the body returned for every non-2xx response.
type Error struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// APIError carries an HTTP status alongside a message, so a handler can return
// one value and let WriteError decide the status.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func Fail(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

var (
	ErrNotFound     = Fail(http.StatusNotFound, "not_found", "no existe")
	ErrUnauthorized = Fail(http.StatusUnauthorized, "unauthorized", "no autenticado")
	ErrForbidden    = Fail(http.StatusForbidden, "forbidden", "sin permiso")
)

// JSON writes v as the response body.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpx: encode response: %v", err)
	}
}

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// WriteError renders err. Anything that is not an APIError becomes a 500 with a
// generic message: internal failures must never leak a driver error to a client.
func WriteError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		JSON(w, apiErr.Status, Error{Error: apiErr.Message, Code: apiErr.Code})
		return
	}
	log.Printf("httpx: unhandled error: %v", err)
	JSON(w, http.StatusInternalServerError, Error{Error: "error interno", Code: "internal"})
}

// Decode reads a JSON body into v, rejecting unknown fields so a typo in a
// client payload fails loudly instead of being silently dropped.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Fail(http.StatusBadRequest, "bad_request", "cuerpo inválido: "+err.Error())
	}
	return nil
}
