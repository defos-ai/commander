package actions

import (
	"errors"

	actionmodel "commander/framework/domain/actions"
	"commander/providers/fs/controller/internal/catalog"
)

// ActionFsStore persists catalog metadata learned from agents in root/actions.json.
// Readiness is stored separately for each agent in its inventory snapshot.
type ActionFsStore struct {
	*catalog.Store[actionmodel.Action]
}

var _ actionmodel.ActionStore = (*ActionFsStore)(nil)

// NewActionFsStore uses state/database/ when root is empty.
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
