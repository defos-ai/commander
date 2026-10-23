package secrets

import (
	"context"
	"errors"
	"reflect"
	"time"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type RequestMongoStore struct{ database *store.MongoStore }

var _ secretmodel.RequestStore = (*RequestMongoStore)(nil)

// NewRequestMongoStore prepares durable request and delivery identities. The
// caller owns the database client; writes require MongoDB transactions.
func NewRequestMongoStore(ctx context.Context, database *store.MongoStore) (*RequestMongoStore, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("secret request database is required")
	}
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "delivery_id", Value: 1}},
			Options: options.Index().SetName("secret_delivery_id").SetUnique(true).
				SetPartialFilterExpression(bson.M{"delivery_id": bson.M{"$type": "string"}})},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "_id", Value: 1}},
			Options: options.Index().SetName("secret_pending_requests")},
	}
	if err := database.EnsureIndexes(ctx, requestsCollection, indexes); err != nil {
		return nil, err
	}
	return &RequestMongoStore{database: database}, nil
}

func (s *RequestMongoStore) Create(ctx context.Context, request secretmodel.Request) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	record, err := secretmodel.NewRequestRecord(request)
	if err != nil {
		return err
	}
	document, err := encodeRequest(record)
	if err != nil {
		return err
	}
	return s.write(ctx, func(tx context.Context) (bool, error) {
		current, err := s.Get(tx, record.Request.ID)
		if err == nil {
			if current.MatchesRequest(record.Request) {
				return false, nil
			}
			return false, secretmodel.ErrRequestConflict
		}
		if !errors.Is(err, secretmodel.ErrUnknownRequest) {
			return false, err
		}
		err = s.database.Insert(tx, requestsCollection, document)
		return err == nil, err
	})
}

func (s *RequestMongoStore) Get(ctx context.Context, requestID string) (secretmodel.RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return secretmodel.RequestRecord{}, err
	}
	if !id.Valid(requestID) {
		return secretmodel.RequestRecord{}, errors.New("secret request requires a canonical UUID")
	}
	var document requestDocument
	if err := s.database.GetByID(ctx, requestsCollection, requestID, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = secretmodel.ErrUnknownRequest
		}
		return secretmodel.RequestRecord{}, err
	}
	return document.decode()
}

func (s *RequestMongoStore) ListPending(ctx context.Context) ([]secretmodel.RequestRecord, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	var documents []requestDocument
	err := s.database.Find(ctx, requestsCollection, bson.M{"status": secretmodel.Pending}, &documents,
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	result := make([]secretmodel.RequestRecord, 0, len(documents))
	for _, document := range documents {
		record, err := document.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (s *RequestMongoStore) SaveDelivery(ctx context.Context, delivery secretmodel.Delivery) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := delivery.Validate(); err != nil {
		return err
	}
	delivery = delivery.Clone()
	return s.update(ctx, delivery.RequestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.WithDelivery(delivery)
	})
}

func (s *RequestMongoStore) Acknowledge(ctx context.Context, installation secretmodel.Installation, now time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := installation.Validate(); err != nil {
		return err
	}
	return s.update(ctx, installation.RequestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Acknowledge(installation, now)
	})
}

func (s *RequestMongoStore) Cancel(ctx context.Context, requestID string, now time.Time) error {
	return s.update(ctx, requestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Cancel(now)
	})
}

func (s *RequestMongoStore) Expire(ctx context.Context, requestID string, now time.Time) error {
	return s.update(ctx, requestID, func(record secretmodel.RequestRecord) (secretmodel.RequestRecord, error) {
		return record.Expire(now)
	})
}

func (s *RequestMongoStore) update(ctx context.Context, requestID string, change func(secretmodel.RequestRecord) (secretmodel.RequestRecord, error)) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	return s.write(ctx, func(tx context.Context) (bool, error) {
		current, err := s.Get(tx, requestID)
		if err != nil {
			return false, err
		}
		next, err := change(current)
		if err != nil {
			return false, err
		}
		if reflect.DeepEqual(current, next) {
			return false, nil
		}
		document, err := encodeRequest(next)
		if err != nil {
			return false, err
		}
		result, err := s.database.Replace(tx, requestsCollection, bson.M{"_id": requestID}, document, false)
		if err != nil {
			return false, err
		}
		if result.MatchedCount != 1 {
			return false, secretmodel.ErrRequestConflict
		}
		return true, nil
	})
}

func (s *RequestMongoStore) write(ctx context.Context, fn func(context.Context) (bool, error)) error {
	for attempt := range 2 {
		wrote := false
		err := s.database.WithTransaction(ctx, func(tx context.Context) error {
			var err error
			wrote, err = fn(tx)
			return err
		})
		if err = writeError(wrote, err); errors.Is(err, secretmodel.ErrWriteUncertain) {
			return err
		}
		if !mongo.IsDuplicateKeyError(err) {
			return err
		}
		// Retry an insert/delivery-ID race against a fresh snapshot. Exact
		// retries remain successful; conflicting immutable identities do not.
		if attempt == 1 {
			return secretmodel.ErrRequestConflict
		}
	}
	return secretmodel.ErrRequestConflict
}
