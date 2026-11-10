package models

type AboutResponse struct {
	Runtime     string `json:"runtime"`
	Application string `json:"application"`
	InstanceID  string `json:"instance_id,omitempty"`
}
