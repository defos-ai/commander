package consumers

import (
	"errors"
	"time"
)

const maxAgentClockSkew = 5 * time.Second

func freshObservation(observedAt, receivedAt time.Time, lifetime time.Duration) (bool, error) {
	if observedAt.IsZero() || observedAt.After(receivedAt.Add(maxAgentClockSkew)) {
		return false, errors.New("agent observation timestamp is missing or too far in the future")
	}
	return observedAt.After(receivedAt.Add(-lifetime)), nil
}
