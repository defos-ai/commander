package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
)

func decodeRequests(data []byte) ([]secretmodel.RequestRecord, error) {
	var records []secretmodel.RequestRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return nil, errors.New("invalid secret request collection encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || records == nil {
		return nil, errors.New("secret request collection must contain one JSON array")
	}
	requests := make(map[string]bool, len(records))
	deliveries := make(map[string]bool, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return nil, errors.New("invalid stored secret request")
		}
		if requests[record.Request.ID] {
			return nil, errors.New("secret request collection contains duplicate request IDs")
		}
		requests[record.Request.ID] = true
		if record.Delivery != nil {
			if deliveries[record.Delivery.ID] {
				return nil, errors.New("secret request collection contains duplicate delivery IDs")
			}
			deliveries[record.Delivery.ID] = true
		}
	}
	return records, nil
}

func findRequest(records []secretmodel.RequestRecord, requestID string) int {
	for index, record := range records {
		if record.Request.ID == requestID {
			return index
		}
	}
	return -1
}
