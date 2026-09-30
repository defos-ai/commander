package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentmodel "commander/framework/domain/agents"
	"commander/framework/id"
	"commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const collection = "agents"

type AgentMongoStore struct {
	database *store.MongoStore
}

var _ agentmodel.AgentStore = (*AgentMongoStore)(nil)

type agentDocument struct {
	ID             string                   `bson:"_id"`
	Snapshot       agentmodel.AgentSnapshot `bson:",inline"`
	LastSeen       inventoryTime            `bson:"last_seen"`
	LastHeartbeat  inventoryTime            `bson:"last_heartbeat"`
	LeaseExpiresAt inventoryTime            `bson:"lease_expires_at"`
	Stale          bool                     `bson:"stale"`
}

// BSON dates have millisecond precision. Keep the remainder for observation
// ordering and exact heartbeat cutoffs without exposing storage fields to callers.
type inventoryTime struct {
	Time        time.Time `bson:"time"`
	Nanoseconds int32     `bson:"nanoseconds"`
}

func NewAgentMongoStore(ctx context.Context, database *store.MongoStore) (*AgentMongoStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{{
		Keys: bson.D{{Key: "last_heartbeat.time", Value: 1}, {Key: "last_heartbeat.nanoseconds", Value: 1}},
	}}); err != nil {
		return nil, err
	}
	return &AgentMongoStore{database: database}, nil
}

func (s *AgentMongoStore) Upsert(ctx context.Context, record agentmodel.Agent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInventoryRecord(record); err != nil {
		return err
	}
	document := agentDocument{
		ID: record.ID, Snapshot: record.AgentSnapshot,
		LastSeen: inventoryTimestamp(record.LastSeen), LastHeartbeat: inventoryTimestamp(record.LastHeartbeat),
		LeaseExpiresAt: inventoryTimestamp(record.LeaseExpiresAt), Stale: record.Stale,
	}
	_, err := s.database.Replace(ctx, collection, bson.M{"_id": record.ID}, document, true)
	return err
}

func (s *AgentMongoStore) Get(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.Agent{}, err
	}
	if !id.ValidAgent(agentID) {
		return agentmodel.Agent{}, agentmodel.ErrUnknownAgent
	}
	var document agentDocument
	if err := s.database.GetByID(ctx, collection, agentID, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return agentmodel.Agent{}, agentmodel.ErrUnknownAgent
		}
		return agentmodel.Agent{}, err
	}
	return document.decode()
}

func (s *AgentMongoStore) List(ctx context.Context) ([]agentmodel.Agent, error) {
	var documents []agentDocument
	if err := s.database.Find(ctx, collection, bson.D{}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
		return nil, err
	}
	result := make([]agentmodel.Agent, 0, len(documents))
	for _, document := range documents {
		record, err := document.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (s *AgentMongoStore) DeleteExpired(ctx context.Context, cutoff time.Time) ([]agentmodel.Agent, error) {
	deadline := inventoryTimestamp(cutoff)
	filter := bson.M{"$or": bson.A{
		bson.M{"last_heartbeat.time": bson.M{"$lt": deadline.Time}},
		bson.M{"last_heartbeat.time": deadline.Time, "last_heartbeat.nanoseconds": bson.M{"$lte": deadline.Nanoseconds}},
		bson.M{"last_heartbeat.time": time.Time{}, "last_heartbeat.nanoseconds": 0},
	}}
	deleted := []agentmodel.Agent{}
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var document agentDocument
		// Match and remove together so a heartbeat renewal cannot be deleted
		// using an older observation. The returned snapshot is the removed one.
		err := s.database.FindOneAndDelete(ctx, collection, filter, &document)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return deleted, nil
		}
		if err != nil {
			return deleted, err
		}
		record, err := document.decode()
		if err != nil {
			return deleted, err
		}
		deleted = append(deleted, record)
	}
}

func (d agentDocument) decode() (agentmodel.Agent, error) {
	if d.ID != d.Snapshot.ID {
		return agentmodel.Agent{}, errors.New("stored agent ID does not match its snapshot")
	}
	record := agentmodel.Agent{AgentSnapshot: d.Snapshot, Stale: d.Stale}
	for _, field := range []struct {
		name  string
		value inventoryTime
		out   *time.Time
	}{
		{"last_seen", d.LastSeen, &record.LastSeen},
		{"last_heartbeat", d.LastHeartbeat, &record.LastHeartbeat},
		{"lease_expires_at", d.LeaseExpiresAt, &record.LeaseExpiresAt},
	} {
		value, err := field.value.decode()
		if err != nil {
			return agentmodel.Agent{}, fmt.Errorf("invalid stored agent %q %s: %w", d.ID, field.name, err)
		}
		*field.out = value
	}
	if err := validateInventoryRecord(record); err != nil {
		return agentmodel.Agent{}, fmt.Errorf("invalid stored agent %q: %w", d.ID, err)
	}
	return record, nil
}

func inventoryTimestamp(value time.Time) inventoryTime {
	return inventoryTime{Time: value.UTC().Truncate(time.Millisecond), Nanoseconds: int32(value.Nanosecond() % int(time.Millisecond))}
}

func (value inventoryTime) decode() (time.Time, error) {
	if value.Nanoseconds < 0 || value.Nanoseconds >= int32(time.Millisecond) || value.Time.Nanosecond()%int(time.Millisecond) != 0 {
		return time.Time{}, errors.New("invalid timestamp precision")
	}
	return value.Time.UTC().Add(time.Duration(value.Nanoseconds)), nil
}

func validateInventoryRecord(record agentmodel.Agent) error {
	if !id.ValidAgent(record.ID) {
		return errors.New("invalid inventory agent ID")
	}
	if record.LastSeen.IsZero() {
		return errors.New("inventory observation must have a receipt timestamp")
	}
	if err := record.Server.Validate(); err != nil {
		return err
	}
	// RawMessage fields are BSON binary; check their JSON syntax before persisting.
	_, err := json.Marshal(record)
	return err
}
