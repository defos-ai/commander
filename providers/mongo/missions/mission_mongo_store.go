package missions

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const collection = "missions"

type MissionMongoStore struct {
	database *store.MongoStore
}

var _ missions.MissionStore = (*MissionMongoStore)(nil)

// JSON schemas stay as raw bytes in BSON so their numbers and extension fields
// round-trip exactly, without adding Mongo-specific codecs to the framework.
type missionDocument struct {
	ID      string           `bson:"_id"`
	Mission missions.Mission `bson:",inline"`
}

func NewMissionMongoStore(database *store.MongoStore) *MissionMongoStore {
	return &MissionMongoStore{database: database}
}

func (s *MissionMongoStore) Create(ctx context.Context, mission missions.Mission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := mission.Validate(); err != nil {
		return err
	}
	err := s.database.Insert(ctx, collection, missionDocument{ID: mission.Name, Mission: mission})
	if duplicateMissionID(err) {
		return missions.ErrMissionExists
	}
	return err
}

// Only the mission-name primary key represents an existing mission. Preserve
// storage errors caused by any unrelated unique index.
func duplicateMissionID(err error) bool {
	var failure mongo.WriteException
	if !errors.As(err, &failure) || len(failure.WriteErrors) != 1 {
		return false
	}
	write := failure.WriteErrors[0]
	if !mongo.IsDuplicateKeyError(write) {
		return false
	}
	pattern, ok := write.Raw.Lookup("keyPattern").DocumentOK()
	if !ok {
		return false
	}
	keys, err := pattern.Elements()
	return err == nil && len(keys) == 1 && keys[0].Key() == "_id"
}

func (s *MissionMongoStore) Upsert(ctx context.Context, mission missions.Mission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := mission.Validate(); err != nil {
		return err
	}
	_, err := s.database.Replace(ctx, collection, bson.M{"_id": mission.Name}, missionDocument{ID: mission.Name, Mission: mission}, true)
	return err
}

func (s *MissionMongoStore) Get(ctx context.Context, name string) (missions.Mission, error) {
	var document missionDocument
	if err := s.database.GetByID(ctx, collection, name, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return missions.Mission{}, missions.ErrUnknownMission
		}
		return missions.Mission{}, err
	}
	return document.decode()
}

func (s *MissionMongoStore) List(ctx context.Context) ([]missions.Mission, error) {
	var documents []missionDocument
	if err := s.database.Find(ctx, collection, bson.D{}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
		return nil, err
	}
	result := make([]missions.Mission, 0, len(documents))
	for _, document := range documents {
		mission, err := document.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, mission)
	}
	return result, nil
}

func (d missionDocument) decode() (missions.Mission, error) {
	if d.ID != d.Mission.Name {
		return missions.Mission{}, errors.New("stored mission ID does not match its name")
	}
	if err := d.Mission.Validate(); err != nil {
		return missions.Mission{}, fmt.Errorf("invalid stored mission %q: %w", d.ID, err)
	}
	return d.Mission, nil
}
