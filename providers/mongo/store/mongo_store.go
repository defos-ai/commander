package store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type MongoStore struct {
	client   *mongo.Client
	database *mongo.Database
}

func Open(ctx context.Context, uri string) (*MongoStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Mongo seed lists are not standard URL authorities; the driver validates them.
	scheme, address, hasScheme := strings.Cut(uri, "://")
	authority, path, hasDatabase := strings.Cut(address, "/")
	if !hasScheme || (scheme != "mongodb" && scheme != "mongodb+srv") || authority == "" {
		return nil, errors.New("MongoDB URL must use mongodb:// or mongodb+srv:// with a hostname")
	}
	path, _, _ = strings.Cut(path, "?")
	database, err := url.PathUnescape(path)
	if err != nil || !hasDatabase || database == "" || strings.Contains(database, "/") {
		return nil, errors.New("MongoDB URL must include a database name")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).
		SetBSONOptions(&options.BSONOptions{UseJSONStructTags: true}).
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, &connectionError{cause: err}
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(cleanup)
		return nil, &connectionError{cause: err}
	}
	return &MongoStore{client: client, database: client.Database(database)}, nil
}

// Driver initialization errors may contain sensitive URI options. Keep their
// printable form safe while retaining the cause for errors.Is/As inspection.
type connectionError struct{ cause error }

func (e *connectionError) Error() string {
	return "MongoDB initialization failed; check connection settings and database availability"
}

func (e *connectionError) Unwrap() error { return e.cause }

// Callers build BSON keys/operators in provider code; request input stays in
// typed values. This method does not sanitize client-decoded query documents.
func (s *MongoStore) Find(ctx context.Context, collection string, filter, out any, opts ...options.Lister[options.FindOptions]) error {
	cursor, err := s.database.Collection(collection).Find(ctx, filter, opts...)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	return cursor.All(ctx, out)
}

// Filters are built by provider code; request-derived IDs are string values,
// never BSON keys or operators. Do not pass client-decoded query documents.
func (s *MongoStore) FindOne(ctx context.Context, collection string, filter, out any, opts ...options.Lister[options.FindOneOptions]) error {
	return s.database.Collection(collection).FindOne(ctx, filter, opts...).Decode(out)
}

func (s *MongoStore) GetByID(ctx context.Context, collection, id string, out any) error {
	return s.FindOne(ctx, collection, bson.M{"_id": id}, out)
}

func (s *MongoStore) Insert(ctx context.Context, collection string, document any) error {
	_, err := s.database.Collection(collection).InsertOne(ctx, document)
	return err
}

func (s *MongoStore) Update(ctx context.Context, collection string, filter, update any) (*mongo.UpdateResult, error) {
	return s.database.Collection(collection).UpdateOne(ctx, filter, update)
}

func (s *MongoStore) Upsert(ctx context.Context, collection string, filter, update any) (*mongo.UpdateResult, error) {
	return s.database.Collection(collection).UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
}

// Keep filter keys/operators provider-defined and request input in typed values.
// The replacement is a stored record, not an update expression.
func (s *MongoStore) Replace(ctx context.Context, collection string, filter, replacement any, upsert bool) (*mongo.UpdateResult, error) {
	return s.database.Collection(collection).ReplaceOne(ctx, filter, replacement, options.Replace().SetUpsert(upsert))
}

func (s *MongoStore) Delete(ctx context.Context, collection string, filter any) (*mongo.DeleteResult, error) {
	return s.database.Collection(collection).DeleteMany(ctx, filter)
}

func (s *MongoStore) FindOneAndDelete(ctx context.Context, collection string, filter, out any) error {
	return s.database.Collection(collection).FindOneAndDelete(ctx, filter).Decode(out)
}

func (s *MongoStore) EnsureIndexes(ctx context.Context, collection string, indexes []mongo.IndexModel) error {
	_, err := s.database.Collection(collection).Indexes().CreateMany(ctx, indexes)
	return err
}

// The callback may run again after a transaction conflict. Use its context for
// store calls, return their errors, and keep external effects outside it.
func (s *MongoStore) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fn(ctx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}

func (s *MongoStore) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}
