package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func Decode(data []byte, destination any) error {
	if err := ValidateObject(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(destination); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single JSON object")
	}
	if v, ok := destination.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}

func ValidateObject(data []byte) error {
	t := bytes.TrimSpace(data)
	if len(t) == 0 || t[0] != '{' || !json.Valid(t) {
		return errors.New("expected a JSON object")
	}
	return nil
}
