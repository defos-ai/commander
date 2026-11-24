package models

type TaskSelectorRequest struct {
	Hostname string `json:"hostname"`
}

type TaskSelectorResponse struct {
	Hostname string `json:"hostname"`
}
