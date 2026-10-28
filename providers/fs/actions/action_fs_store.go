package actions

import (
	"errors"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/providers/fs/internal/catalog"
)

type ActionFsStore struct {
	*catalog.Store[actionmodel.Action]
}

var _ actionmodel.ActionStore = (*ActionFsStore)(nil)

func NewActionFsStore(root string) (*ActionFsStore, error) {
	catalog, err := catalog.New(root, "actions.json", func(t actionmodel.Action) string { return t.Name }, validateCatalogAction, actionmodel.ErrUnknownAction)
	if err != nil {
		return nil, err
	}
	return &ActionFsStore{catalog}, nil
}

func validateCatalogAction(description actionmodel.Action) error {
	if description.Status != "" && description.Status != actionmodel.Unknown {
		return errors.New("action presence belongs to an agent, not the action catalog")
	}
	return description.Validate()
}
