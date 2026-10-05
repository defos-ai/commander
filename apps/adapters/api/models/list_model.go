package models

type ListResponse[T any] struct {
	Items []T `json:"items"`
}
