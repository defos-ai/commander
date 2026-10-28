package operations

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain"
)

func pageResponse[T any](items []T, cursor string) models.PageResponse[T] {
	return models.PageResponse[T]{Items: endpoints.ListResponse(items).Items, NextCursor: cursor}
}

func readPage(w http.ResponseWriter, r *http.Request, filters ...string) (cursor string, limit int, ok bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	for name, values := range query {
		if (name != "cursor" && name != "limit" && !slices.Contains(filters, name)) || len(values) != 1 {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	limit = 100
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || domain.ValidatePageLimit(limit) != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
	}
	return query.Get("cursor"), limit, true
}
