package faultcontroller

import (
	"context"
	"errors"
	"net/http"

	"github.com/defos-ai/commander/framework/domain/actions"
	sessionmodel "github.com/defos-ai/commander/framework/domain/sessions"
)

type Catalog struct {
	actions.ActionStore
	Peer string
}

func (c Catalog) List(ctx context.Context) ([]actions.Action, error) {
	if err := probe(ctx, c.Peer, "/catalog"); err != nil {
		return nil, err
	}
	return c.ActionStore.List(ctx)
}

type SessionStore struct {
	sessionmodel.Store
	Peer string
}

func (s SessionStore) Apply(ctx context.Context, change sessionmodel.Change) (sessionmodel.Record, error) {
	if err := probe(ctx, s.Peer, "/sessions"); err != nil {
		return sessionmodel.Record{}, err
	}
	return s.Store.Apply(ctx, change)
}

func probe(ctx context.Context, peer, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer+path, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		return errors.New("fixture storage temporarily unavailable")
	}
	return nil
}
