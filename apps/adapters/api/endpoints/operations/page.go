package operations

import (
	"net/http"
	"net/url"
	"strconv"

	"commander/adapters/api/endpoints"
	"commander/adapters/api/models"
)

func pageResponse[T any](items []T, cursor string) models.PageResponse[T] {
	return models.PageResponse[T]{Items: endpoints.ListResponse(items).Items, NextCursor: cursor}
}

func readPage(w http.ResponseWriter, r *http.Request) (cursor string, limit int, ok bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	for name, values := range query {
		if (name != "cursor" && name != "limit") || len(values) != 1 {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	limit = 100
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 1000 {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
	}
	return query.Get("cursor"), limit, true
}
