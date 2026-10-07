package missions

import (
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/providers/fs/internal/catalog"
)

// MissionFsStore stores mission records in root/missions.json.
type MissionFsStore struct {
	*catalog.Store[missions.Mission]
}

var _ missions.MissionStore = (*MissionFsStore)(nil)

// NewMissionFsStore uses state/database/ when root is empty.
func NewMissionFsStore(root string) (*MissionFsStore, error) {
	catalog, err := catalog.New(root, "missions.json", func(m missions.Mission) string { return m.Name }, missions.Mission.Validate, missions.ErrUnknownMission)
	if err != nil {
		return nil, err
	}
	return &MissionFsStore{catalog}, nil
}
