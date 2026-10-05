package mappings

import (
	"commander/adapters/api/models"
	"commander/framework/domain/artifacts"
)

func ToArtifactResponse(artifact artifacts.Artifact) models.ArtifactResponse {
	var files []models.ArtifactFileResponse
	if artifact.Files != nil {
		files = make([]models.ArtifactFileResponse, len(artifact.Files))
		for i, file := range artifact.Files {
			files[i] = models.ArtifactFileResponse{
				ID:        file.ID,
				Name:      file.Name,
				MediaType: file.MediaType,
				Size:      file.Size,
				SHA256:    file.SHA256,
			}
		}
	}
	return models.ArtifactResponse{
		ID:         artifact.ID,
		Name:       artifact.Name,
		AgentID:    artifact.AgentID,
		ActionName: artifact.ActionName,
		CommandID:  artifact.CommandID,
		CreatedAt:  artifact.CreatedAt,
		Files:      files,
	}
}

func ToArtifactResponses(artifacts []artifacts.Artifact) []models.ArtifactResponse {
	if artifacts == nil {
		return nil
	}
	responses := make([]models.ArtifactResponse, len(artifacts))
	for i, artifact := range artifacts {
		responses[i] = ToArtifactResponse(artifact)
	}
	return responses
}
