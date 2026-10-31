package mappings

import (
	"bytes"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/assessments"
	"github.com/defos-ai/commander/framework/domain/observations"
	"slices"
)

func ToAssessment(value *models.AssessmentSnapshot) *assessments.Snapshot {
	if value == nil {
		return nil
	}
	out := &assessments.Snapshot{TargetID: value.TargetID, TargetName: value.TargetName, Revision: value.Revision, NetworkID: value.NetworkID,
		Scope: assessments.Scope{Networks: make([]assessments.Network, len(value.Scope.Networks)), Domains: make([]assessments.Domain, len(value.Scope.Domains))}}
	for i, network := range value.Scope.Networks {
		out.Scope.Networks[i] = assessments.Network{ID: network.ID, Name: network.Name, CIDRs: slices.Clone(network.CIDRs)}
	}
	for i, domain := range value.Scope.Domains {
		out.Scope.Domains[i] = assessments.Domain{Hostname: domain.Hostname, IncludeSubdomains: domain.IncludeSubdomains}
	}
	return out
}
func ToAssessmentResponse(value *assessments.Snapshot) *models.AssessmentSnapshot {
	if value == nil {
		return nil
	}
	out := &models.AssessmentSnapshot{TargetID: value.TargetID, TargetName: value.TargetName, Revision: value.Revision, NetworkID: value.NetworkID,
		Scope: models.AssessmentScope{Networks: make([]models.AssessmentNetwork, len(value.Scope.Networks)), Domains: make([]models.AssessmentDomain, len(value.Scope.Domains))}}
	for i, network := range value.Scope.Networks {
		out.Scope.Networks[i] = models.AssessmentNetwork{ID: network.ID, Name: network.Name, CIDRs: slices.Clone(network.CIDRs)}
	}
	for i, domain := range value.Scope.Domains {
		out.Scope.Domains[i] = models.AssessmentDomain{Hostname: domain.Hostname, IncludeSubdomains: domain.IncludeSubdomains}
	}
	return out
}
func copyObservationPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
func observationEndpoint(value *observations.Endpoint) *models.ObservationEndpointResponse {
	if value == nil {
		return nil
	}
	return &models.ObservationEndpointResponse{NetworkID: value.NetworkID, Address: value.Address}
}
func ToObservationsResponse(value *observations.Batch) *models.ObservationBatchResponse {
	if value == nil {
		return nil
	}
	out := &models.ObservationBatchResponse{Version: value.Version, Records: make([]models.ObservationResponse, len(value.Records))}
	for i, record := range value.Records {
		out.Records[i] = models.ObservationResponse{
			ID: record.ID, Kind: record.Kind, ObservedAt: copyObservationPointer(record.ObservedAt), NetworkID: record.NetworkID,
			Address: record.Address, Hostname: record.Hostname, Port: copyObservationPointer(record.Port), Transport: record.Transport,
			Protocol: record.Protocol, Product: record.Product, Version: record.Version, State: record.State, IssueID: record.IssueID,
			Title: record.Title, Severity: record.Severity, Description: record.Description, Evidence: record.Evidence,
			Source: observationEndpoint(record.Source), Destination: observationEndpoint(record.Destination), Relationship: record.Relationship,
			Directed: copyObservationPointer(record.Directed), Details: bytes.Clone(record.Details),
		}
		if record.Identity != nil {
			out.Records[i].Identity = &models.ObservationIdentityResponse{Namespace: record.Identity.Namespace, Value: record.Identity.Value, Evidence: record.Identity.Evidence}
		}
	}
	return out
}
