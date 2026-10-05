package api_test

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"

	"commander/framework/domain/operations"
)

func TestOperationEventsUseExclusiveSequenceCursor(t *testing.T) {
	fixture := newFixture(t)
	op := operationRecord(1)
	// Stored order and sequence gaps must not affect cursor ordering.
	op.Events = []operations.Event{
		{Sequence: 12, OperationID: op.ID, Type: operations.TaskFinished, TaskID: recordID(20), Status: operations.Running, Message: "Links reviewed", CreatedAt: op.UpdatedAt},
		{Sequence: 2, OperationID: op.ID, Type: operations.QuestionAsked, QuestionID: recordID(30), Status: operations.Waiting, Message: "Confirm scope", CreatedAt: op.CreatedAt},
		{Sequence: 8, OperationID: op.ID, Type: operations.TaskDispatched, TaskID: recordID(20), Status: operations.Running, Message: "Reviewing links", CreatedAt: op.UpdatedAt},
	}
	op = saveOperation(t, operationStore(t, fixture), op)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID + "/events"
	before := operationBytes(t, fixture, op.ID)

	first := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"?limit=2", authenticate))
	second := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"?cursor="+first.NextCursor+"&limit=2", authenticate))
	gap := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"?cursor=3", authenticate))
	zero := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"?cursor=0&limit=1", authenticate))

	if !reflect.DeepEqual(first.Items, []operations.Event{op.Events[1], op.Events[2]}) || first.NextCursor != "8" {
		t.Fatalf("events were not returned in ascending sequence order: %+v", first)
	}
	if !reflect.DeepEqual(second.Items, op.Events[:1]) || second.NextCursor != "12" {
		t.Fatalf("second page did not start strictly after its cursor: %+v", second)
	}
	if !reflect.DeepEqual(gap.Items, []operations.Event{op.Events[2], op.Events[0]}) || gap.NextCursor != "12" {
		t.Fatalf("cursor between saved sequences returned the wrong events: %+v", gap)
	}
	if !reflect.DeepEqual(zero.Items, op.Events[1:2]) || zero.NextCursor != "2" {
		t.Fatalf("zero cursor did not select the first event: %+v", zero)
	}
	for _, cursor := range []string{"12", "0012", "100", "18446744073709551615"} {
		page := decodeResponse[eventPage](t, server.request(t, http.MethodGet, base+"?cursor="+cursor, authenticate))
		if page.Items == nil || len(page.Items) != 0 || page.NextCursor != cursor {
			t.Fatalf("empty event page changed cursor %q or returned a null array: %+v", cursor, page)
		}
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("event queries changed operation storage")
	}
}

type eventPage struct {
	Items      []operations.Event `json:"items"`
	NextCursor string             `json:"next_cursor"`
}
