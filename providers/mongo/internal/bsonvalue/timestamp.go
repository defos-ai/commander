package bsonvalue

import (
	"errors"
	"time"
)

// BSON dates have millisecond precision. Keep the remainder so stored envelopes,
// observation ordering, and heartbeat cutoffs retain their exact timestamps.
type Timestamp struct {
	Time        time.Time `bson:"time"`
	Nanoseconds int32     `bson:"nanoseconds"`
}

func NewTimestamp(value time.Time) Timestamp {
	return Timestamp{Time: value.UTC().Truncate(time.Millisecond), Nanoseconds: int32(value.Nanosecond() % int(time.Millisecond))}
}

func (value Timestamp) Decode() (time.Time, error) {
	if value.Nanoseconds < 0 || value.Nanoseconds >= int32(time.Millisecond) || value.Time.Nanosecond()%int(time.Millisecond) != 0 {
		return time.Time{}, errors.New("invalid timestamp precision")
	}
	return value.Time.UTC().Add(time.Duration(value.Nanoseconds)), nil
}
