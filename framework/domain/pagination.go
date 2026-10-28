package domain

import "fmt"

const MaxPageSize = 1000

func ValidatePageLimit(limit int) error {
	if limit < 1 || limit > MaxPageSize {
		return fmt.Errorf("%w: page limit must be between 1 and %d", ErrInvalidInput, MaxPageSize)
	}
	return nil
}
