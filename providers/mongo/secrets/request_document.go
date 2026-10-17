package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
)

const requestsCollection = "secret_requests"

// The JSON aggregate preserves authenticated timestamps and uint64 versions.
// Indexed fields are redundant and must agree with the validated payload.
type requestDocument struct {
	ID         string                    `bson:"_id"`
	Status     secretmodel.RequestStatus `bson:"status"`
	DeliveryID string                    `bson:"delivery_id,omitempty"`
	Data       json.RawMessage           `bson:"data"`
}

func encodeRequest(record secretmodel.RequestRecord) (requestDocument, error) {
	if err := record.Validate(); err != nil {
		return requestDocument{}, err
	}
	document := requestDocument{ID: record.Request.ID, Status: record.Request.Status}
	if record.Delivery != nil {
		document.DeliveryID = record.Delivery.ID
	}
	var err error
	document.Data, err = json.Marshal(record)
	return document, err
}

func (document requestDocument) decode() (secretmodel.RequestRecord, error) {
	var record secretmodel.RequestRecord
	decoder := json.NewDecoder(bytes.NewReader(document.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return secretmodel.RequestRecord{}, errors.New("invalid stored secret request encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return secretmodel.RequestRecord{}, errors.New("stored secret request must contain one JSON value")
	}
	if err := record.Validate(); err != nil {
		return secretmodel.RequestRecord{}, err
	}
	deliveryID := ""
	if record.Delivery != nil {
		deliveryID = record.Delivery.ID
	}
	if record.Request.ID != document.ID || record.Request.Status != document.Status || deliveryID != document.DeliveryID {
		return secretmodel.RequestRecord{}, errors.New("stored secret request does not match its indexed fields")
	}
	return record, nil
}
