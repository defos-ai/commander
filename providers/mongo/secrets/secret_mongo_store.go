package secrets

import (
	"context"
	"errors"
	"math"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type SecretMongoStore struct{ database *store.MongoStore }

var _ secretmodel.SecretStore = (*SecretMongoStore)(nil)

func NewSecretMongoStore(ctx context.Context, database *store.MongoStore) (*SecretMongoStore, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("secret database is required")
	}
	// _id is permanent, while only active records reserve the shared name.
	// Never expire these documents: tombstones prevent delayed resurrection.
	index := mongo.IndexModel{
		Keys: bson.D{{Key: "name", Value: 1}},
		Options: options.Index().SetName("active_secret_name").SetUnique(true).
			SetPartialFilterExpression(bson.M{"active": true}).SetCollation(&options.Collation{Locale: "simple"}),
	}
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{index}); err != nil {
		return nil, err
	}
	return &SecretMongoStore{database: database}, nil
}

func (s *SecretMongoStore) Create(ctx context.Context, secret secretmodel.Secret) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	snapshot := secret.Clone()
	doc, err := encode(snapshot)
	if err != nil {
		return err
	}
	if snapshot.Metadata.Version != 1 || !snapshot.Metadata.CreatedAt.Equal(snapshot.Metadata.UpdatedAt) {
		return errors.New("new secret must have version 1 and equal creation and update times")
	}
	return s.write(ctx, func(tx context.Context) (bool, error) {
		previous, saved, err := s.find(tx, bson.M{"_id": doc.ID})
		if err == nil {
			if previous.Active && equalSecrets(saved, snapshot) {
				return false, nil
			}
			return false, secretmodel.ErrSecretConflict
		}
		if !errors.Is(err, secretmodel.ErrUnknownSecret) {
			return false, err
		}
		if _, _, err := s.find(tx, activeKey(doc.key())); !errors.Is(err, secretmodel.ErrUnknownSecret) {
			if err == nil {
				err = secretmodel.ErrSecretConflict
			}
			return false, err
		}
		err = s.database.Insert(tx, collection, doc)
		return err == nil, err
	})
}

func (s *SecretMongoStore) Get(ctx context.Context, key secretmodel.Key) (secretmodel.Secret, error) {
	if err := checkContext(ctx); err != nil {
		return secretmodel.Secret{}, err
	}
	if err := key.Validate(); err != nil {
		return secretmodel.Secret{}, err
	}
	_, secret, err := s.find(ctx, activeKey(key))
	return secret, err
}

func (s *SecretMongoStore) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
	secret, err := s.Get(ctx, key)
	return secret.Metadata, err
}

func (s *SecretMongoStore) List(ctx context.Context) ([]secretmodel.Metadata, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	var documents []secretDocument
	filter := bson.M{"active": true}
	err := s.database.Find(ctx, collection, filter, &documents,
		options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetCollation(&options.Collation{Locale: "simple"}))
	if err != nil {
		return nil, err
	}
	result := make([]secretmodel.Metadata, 0, len(documents))
	for _, document := range documents {
		secret, err := document.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, secret.Metadata)
	}
	return result, nil
}

func (s *SecretMongoStore) Replace(ctx context.Context, secret secretmodel.Secret, expectedVersion uint64) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	snapshot := secret.Clone()
	doc, err := encode(snapshot)
	if err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || snapshot.Metadata.Version != expectedVersion+1 {
		return secretmodel.ErrSecretConflict
	}
	return s.write(ctx, func(tx context.Context) (bool, error) {
		_, saved, err := s.find(tx, activeKey(doc.key()))
		if err != nil {
			return false, err
		}
		if equalSecrets(saved, snapshot) {
			return false, nil
		}
		a, b := saved.Metadata, snapshot.Metadata
		if a.ID != b.ID || a.Version != expectedVersion || !a.CreatedAt.Equal(b.CreatedAt) || b.UpdatedAt.Before(a.UpdatedAt) {
			return false, secretmodel.ErrSecretConflict
		}
		return s.replace(tx, doc)
	})
}

func (s *SecretMongoStore) Delete(ctx context.Context, key secretmodel.Key, secretID string, expectedVersion uint64) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if !id.Valid(secretID) || expectedVersion == 0 {
		return errors.New("secret deletion requires a canonical UUID and positive version")
	}
	return s.write(ctx, func(tx context.Context) (bool, error) {
		identity, _, err := s.find(tx, bson.M{"_id": secretID})
		if err != nil && !errors.Is(err, secretmodel.ErrUnknownSecret) {
			return false, err
		}
		if err == nil && identity.key() != key {
			return false, secretmodel.ErrSecretConflict
		}
		retired := identityDocument(secretID, key)
		_, saved, err := s.find(tx, activeKey(key))
		if err == nil {
			if saved.Metadata.ID != secretID || saved.Metadata.Version != expectedVersion {
				return false, secretmodel.ErrSecretConflict
			}
			return s.replace(tx, retired)
		}
		if !errors.Is(err, secretmodel.ErrUnknownSecret) {
			return false, err
		}
		if identity.ID != "" {
			return false, nil
		}
		// Retire an absent ID too, before its delayed initial Create can arrive.
		err = s.database.Insert(tx, collection, retired)
		return err == nil, err
	})
}

func activeKey(key secretmodel.Key) bson.M {
	return bson.M{"name": key.Name, "active": true}
}

func (s *SecretMongoStore) find(ctx context.Context, filter bson.M) (secretDocument, secretmodel.Secret, error) {
	var doc secretDocument
	err := s.database.FindOne(ctx, collection, filter, &doc, options.FindOne().SetCollation(&options.Collation{Locale: "simple"}))
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = secretmodel.ErrUnknownSecret
	}
	if err != nil {
		return secretDocument{}, secretmodel.Secret{}, err
	}
	secret, err := doc.decode()
	return doc, secret, err
}

func (s *SecretMongoStore) replace(ctx context.Context, doc secretDocument) (bool, error) {
	result, err := s.database.Replace(ctx, collection, bson.M{"_id": doc.ID, "active": true}, doc, false)
	if err != nil {
		return false, err
	}
	if result.MatchedCount != 1 {
		return false, secretmodel.ErrSecretConflict
	}
	return true, nil
}

func (s *SecretMongoStore) write(ctx context.Context, fn func(context.Context) (bool, error)) error {
	for attempt := range 2 {
		wrote := false
		err := s.database.WithTransaction(ctx, func(tx context.Context) error {
			var err error
			wrote, err = fn(tx) // Reset on every driver retry; retain its error labels.
			return err
		})
		if err = writeError(wrote, err); errors.Is(err, secretmodel.ErrWriteUncertain) {
			return err
		}
		if !mongo.IsDuplicateKeyError(err) {
			return err
		}
		// Another Create/Delete may have won after our snapshot read. A fresh
		// transaction accepts identical retries and classifies actual conflicts.
		if attempt == 1 {
			return secretmodel.ErrSecretConflict
		}
	}
	return secretmodel.ErrSecretConflict
}

func writeError(wrote bool, err error) error {
	if err != nil && wrote {
		return errors.Join(secretmodel.ErrWriteUncertain, err)
	}
	return err
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("secret database requires a context")
	}
	return ctx.Err()
}
