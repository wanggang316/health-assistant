package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// apiError is an error response (§5.1).
type apiError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errInvalid(field, msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: "invalid_request", Message: msg, Details: map[string]any{"field": field}}
}

var (
	errUnauthenticated = &apiError{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "Missing or unknown access token."}
	errTokenExpired    = &apiError{Status: http.StatusUnauthorized, Code: "token_expired", Message: "Access token expired."}
	errSessionRevoked  = &apiError{Status: http.StatusUnauthorized, Code: "session_revoked", Message: "Session revoked."}
	errNotFound        = &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found."}
	errTooLarge        = &apiError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "Request body is too large."}
	errInternal        = &apiError{Status: http.StatusInternalServerError, Code: "internal", Message: "Internal error."}
)

type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes err as an error body. Errors that are not *apiError are
// logged and returned as 500 internal.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		s.log.ErrorContext(r.Context(), "request failed", "request_id", requestID(r.Context()), "err", err)
		ae = errInternal
	}
	var b errorBody
	b.Error.Code, b.Error.Message, b.Error.Details = ae.Code, ae.Message, ae.Details
	b.RequestID = requestID(r.Context())
	writeJSON(w, ae.Status, b)
}

// decodeJSON reads a JSON body of at most 64 KB with no unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge
		}
		return &apiError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Body is not valid JSON: " + err.Error()}
	}
	return nil
}
