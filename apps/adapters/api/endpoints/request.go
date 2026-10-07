package endpoints

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/defos-ai/commander/framework/domain"
)

func RequestHandler(query http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		query(w, r.WithContext(ctx))
	}
}

func ReadJSON(w http.ResponseWriter, r *http.Request, destination any, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			WriteError(w, http.StatusBadRequest, "invalid_request")
		}
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if allowEmpty {
			return true
		}
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return false
	}
	if err := domain.Decode(data, destination); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}
