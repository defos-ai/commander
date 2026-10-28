package fixtures

import (
	"sort"
	"testing"

	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func CollectionDocuments(t *testing.T, database *store.MongoStore, collection string) []string {
	t.Helper()
	var documents []bson.Raw
	if err := database.Find(t.Context(), collection, bson.D{}, &documents); err != nil {
		t.Fatal(err)
	}
	encoded := make([]string, len(documents))
	for i, document := range documents {
		encoded[i] = string(document)
	}
	sort.Strings(encoded)
	return encoded
}

func AllOperationDocuments(t *testing.T, database *store.MongoStore) map[string][]string {
	t.Helper()
	result := make(map[string][]string)
	for _, collection := range []string{"operations", "operation_tasks", "operation_questions", "operation_events"} {
		result[collection] = CollectionDocuments(t, database, collection)
	}
	return result
}
