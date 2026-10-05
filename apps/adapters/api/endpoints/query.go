package endpoints

import (
	"context"
	"net/http"
	"time"
)

func QueryHandler(query http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		query(w, r.WithContext(ctx))
	}
}
