package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/mongo/internal/bsonvalue"
	"github.com/defos-ai/commander/providers/mongo/store"

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
	ObservedAt     bsonvalue.Timestamp      `bson:"observed_at"`
	LastSeen       bsonvalue.Timestamp      `bson:"last_seen"`
	LastHeartbeat  bsonvalue.Timestamp      `bson:"last_heartbeat"`
	LeaseExpiresAt bsonvalue.Timestamp      `bson:"lease_expires_at"`
	Stale          bool                     `bson:"stale"`
	Expired        bool                     `bson:"expired,omitempty"`
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
		ID: record.ID, Snapshot: record.AgentSnapshot, ObservedAt: bsonvalue.NewTimestamp(record.ObservedAt),
		LastSeen: bsonvalue.NewTimestamp(record.LastSeen), LastHeartbeat: bsonvalue.NewTimestamp(record.LastHeartbeat),
		LeaseExpiresAt: bsonvalue.NewTimestamp(record.LeaseExpiresAt), Stale: record.Stale,
	}
	filter := timestampBefore("observed_at", record.ObservedAt, false)
	filter["_id"] = record.ID
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := s.database.Replace(ctx, collection, filter, document, true)
		if !duplicateAgentID(err) {
			return err
		}
		// A nonmatching observation attempts an insert with the existing _id.
		// Read the winner before classifying that conflict: another writer may
		// have inserted an older observation, or expiry may have removed it.
		var saved agentDocument
		if readErr := s.database.GetByID(ctx, collection, record.ID, &saved); readErr != nil {
			if errors.Is(readErr, mongo.ErrNoDocuments) {
				continue
			}
			return readErr
		}
		observed, decodeErr := saved.ObservedAt.Decode()
		if decodeErr != nil {
			return fmt.Errorf("invalid stored agent %q observed_at: %w", saved.ID, decodeErr)
		}
		if !observed.IsZero() && !record.ObservedAt.After(observed) {
			current, decodeErr := saved.decode()
			if decodeErr != nil {
				return decodeErr
			}
			if saved.Expired || current.Status == agentmodel.AgentStopped ||
				!record.ObservedAt.Equal(observed) || !record.LastHeartbeat.After(current.LastHeartbeat) ||
				!reflect.DeepEqual(record.AgentSnapshot, current.AgentSnapshot) {
				return agentmodel.ErrStaleObservation
			}
			// Renew liveness only for the same snapshot at an equal mark.
			// Matching both clocks and receipt time prevents a writer from losing
			// its newer heartbeat, lifecycle snapshot, or last-seen metadata.
			renewal := saved
			renewal.LastHeartbeat = bsonvalue.NewTimestamp(record.LastHeartbeat)
			renewal.LeaseExpiresAt = bsonvalue.NewTimestamp(record.LeaseExpiresAt)
			if record.LastSeen.After(current.LastSeen) {
				renewal.LastSeen = bsonvalue.NewTimestamp(record.LastSeen)
			}
			renewal.Stale = false
			renewFilter := bson.M{"_id": record.ID, "observed_at": saved.ObservedAt,
				"last_heartbeat": saved.LastHeartbeat, "last_seen": saved.LastSeen,
				"expired": bson.M{"$ne": true}, "status": bson.M{"$ne": agentmodel.AgentStopped}}
			result, err := s.database.Replace(ctx, collection, renewFilter, renewal, false)
			if err != nil {
				return err
			}
			if result.MatchedCount != 0 {
				return nil
			}
			continue
		}
		// The current observation is eligible. A conditional replacement avoids
		// another insert race; if it disappeared or advanced, reconsider it.
		result, err := s.database.Replace(ctx, collection, filter, document, false)
		if err != nil {
			return err
		}
		if result.MatchedCount != 0 {
			return nil
		}
	}
}

// Only the primary-key conflict represents a rejected observation predicate.
// Preserve errors from unrelated unique indexes instead of retrying them.
func duplicateAgentID(err error) bool {
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
	if document.Expired {
		return agentmodel.Agent{}, agentmodel.ErrUnknownAgent
	}
	return document.decode()
}

func (s *AgentMongoStore) List(ctx context.Context) ([]agentmodel.Agent, error) {
	var documents []agentDocument
	if err := s.database.Find(ctx, collection, bson.M{"expired": bson.M{"$ne": true}}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
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
	deleted := []agentmodel.Agent{}
	if err := ctx.Err(); err != nil {
		return deleted, err
	}
	// A tombstone only protects observations newer than this sweep's horizon.
	// Match its current state so a concurrent reactivation is never pruned.
	prunable := timestampBefore("observed_at", cutoff, true)
	prunable["expired"] = true
	if _, err := s.database.Delete(ctx, collection, prunable); err != nil {
		return deleted, err
	}
	eligible := timestampBefore("last_heartbeat", cutoff, true)
	eligible["expired"] = bson.M{"$ne": true}
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var document agentDocument
		err := s.database.FindOne(ctx, collection, eligible, &document)
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
		if record.ObservedAt.After(cutoff) {
			// Source observations strictly advance, so their exact timestamp
			// identifies this snapshot. Also guard its heartbeat and active state
			// to serialize expiry with renewal and competing sweepers.
			filter := bson.M{
				"_id": document.ID, "expired": bson.M{"$ne": true},
				"observed_at.time": document.ObservedAt.Time, "observed_at.nanoseconds": document.ObservedAt.Nanoseconds,
				"last_heartbeat.time": document.LastHeartbeat.Time, "last_heartbeat.nanoseconds": document.LastHeartbeat.Nanoseconds,
			}
			result, err := s.database.Update(ctx, collection, filter, bson.M{"$set": bson.M{"expired": true}})
			if err != nil {
				return deleted, err
			}
			if result.MatchedCount == 0 {
				continue
			}
		} else {
			// Physical removal returns the exact snapshot that matched both
			// cutoffs. A renewed heartbeat or newer observation invalidates it.
			filter := bson.M{"_id": document.ID, "$and": bson.A{eligible, timestampBefore("observed_at", cutoff, true)}}
			if err := s.database.FindOneAndDelete(ctx, collection, filter, &document); err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					continue
				}
				return deleted, err
			}
			record, err = document.decode()
			if err != nil {
				return deleted, err
			}
		}
		deleted = append(deleted, record)
	}
}

// Timestamp fields retain the sub-millisecond remainder separately from BSON
// dates. Missing or zero observations have no ordering mark; a missing
// or zero heartbeat has no lease. Both remain eligible independently of cutoff.
func timestampBefore(field string, cutoff time.Time, inclusive bool) bson.M {
	deadline := bsonvalue.NewTimestamp(cutoff)
	comparison := "$lt"
	if inclusive {
		comparison = "$lte"
	}
	return bson.M{"$or": bson.A{
		bson.M{field + ".time": bson.M{"$lt": deadline.Time}},
		bson.M{field + ".time": deadline.Time, field + ".nanoseconds": bson.M{comparison: deadline.Nanoseconds}},
		bson.M{field + ".time": time.Time{}, field + ".nanoseconds": 0},
		bson.M{field: bson.M{"$exists": false}},
	}}
}

func (d agentDocument) decode() (agentmodel.Agent, error) {
	if d.ID != d.Snapshot.ID {
		return agentmodel.Agent{}, errors.New("stored agent ID does not match its snapshot")
	}
	record := agentmodel.Agent{AgentSnapshot: d.Snapshot, Stale: d.Stale}
	for _, field := range []struct {
		name  string
		value bsonvalue.Timestamp
		out   *time.Time
	}{
		{"observed_at", d.ObservedAt, &record.ObservedAt},
		{"last_seen", d.LastSeen, &record.LastSeen},
		{"last_heartbeat", d.LastHeartbeat, &record.LastHeartbeat},
		{"lease_expires_at", d.LeaseExpiresAt, &record.LeaseExpiresAt},
	} {
		value, err := field.value.Decode()
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
