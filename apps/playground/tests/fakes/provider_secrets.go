package fakes

import (
	"encoding/json"
	"net/http"
)

type ProviderSecretOptions struct {
	Name           string
	ExistingStatus string
	ReadStatus     int
	ReadCode       string
	WriteStatus    int
	WriteCode      string
	BlockRead      bool
}

type ProviderSecretRequest struct {
	Method, Path, Username, Password string
	Name, Value                      string
}

type ProviderSecrets struct {
	options  ProviderSecretOptions
	requests chan ProviderSecretRequest
	started  chan struct{}
	canceled chan struct{}
}

func NewProviderSecrets(options ProviderSecretOptions) *ProviderSecrets {
	if options.Name == "" {
		options.Name = "OPENAI_API_KEY"
	}
	return &ProviderSecrets{options: options, requests: make(chan ProviderSecretRequest, 32), started: make(chan struct{}), canceled: make(chan struct{})}
}

func (s *ProviderSecrets) ReadStarted() <-chan struct{}  { return s.started }
func (s *ProviderSecrets) ReadCanceled() <-chan struct{} { return s.canceled }

func (s *ProviderSecrets) Requests() []ProviderSecretRequest {
	var requests []ProviderSecretRequest
	for {
		select {
		case request := <-s.requests:
			requests = append(requests, request)
		default:
			return requests
		}
	}
}

func (s *ProviderSecrets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request := ProviderSecretRequest{Method: r.Method, Path: r.URL.Path}
	request.Username, request.Password, _ = r.BasicAuth()
	readCanceled := false
	defer func() {
		s.requests <- request
		if readCanceled {
			close(s.canceled)
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/"+s.options.Name:
		if s.options.BlockRead {
			close(s.started)
			<-r.Context().Done()
			readCanceled = true
			return
		}
		if s.options.ExistingStatus != "" {
			s.metadata(w, s.options.ExistingStatus)
			return
		}
		status, code := s.options.ReadStatus, s.options.ReadCode
		if status == 0 {
			status, code = http.StatusNotFound, "secret_not_found"
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/secrets":
		var input struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
			return
		}
		request.Name, request.Value = input.Name, input.Value
		status := s.options.WriteStatus
		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		if status == http.StatusCreated {
			s.metadata(w, "available")
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": s.options.WriteCode})
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "route_not_found"})
	}
}

func (s *ProviderSecrets) metadata(w http.ResponseWriter, status string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "da378c90-63eb-46da-b186-552e1e144510", "name": s.options.Name, "version": 1, "status": status,
	})
}
