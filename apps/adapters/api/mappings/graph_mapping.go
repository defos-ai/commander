package mappings

import (
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func ToOperationGraphResponse(operation operations.Operation) models.OperationGraphResponse {
	response := models.OperationGraphResponse{
		OperationID: operation.ID,
		Status:      string(operation.Status),
		UpdatedAt:   operation.UpdatedAt,
		Nodes:       []models.GraphNodeResponse{},
		Edges:       []models.GraphEdgeResponse{},
	}
	nodes := make(map[string]bool)
	edges := make(map[models.GraphEdgeResponse]bool)
	addNode := func(node models.GraphNodeResponse) {
		if !nodes[node.ID] {
			nodes[node.ID] = true
			response.Nodes = append(response.Nodes, node)
		}
	}
	addEdge := func(source, target, kind string) {
		edge := models.GraphEdgeResponse{Source: source, Target: target, Kind: kind}
		if source != target && nodes[source] && nodes[target] && !edges[edge] {
			edges[edge] = true
			response.Edges = append(response.Edges, edge)
		}
	}
	for _, task := range operation.Tasks {
		value := toGraphTaskResponse(task)
		addNode(models.GraphNodeResponse{ID: "task:" + task.ID, Kind: "task", Task: &value})
	}
	for _, task := range operation.Tasks {
		taskID := "task:" + task.ID
		if task.ParentTaskID != "" {
			addEdge("task:"+task.ParentTaskID, taskID, "parent_child")
		}
		if task.Result == nil {
			continue
		}
		for _, artifact := range task.Result.Artifacts {
			artifactID := "artifact:" + artifact.ID
			value := models.GraphArtifactResponse{
				ID: artifact.ID, Name: artifact.Name, AgentID: artifact.AgentID,
				ActionName: artifact.ActionName, CommandID: artifact.CommandID, CreatedAt: artifact.CreatedAt,
			}
			addNode(models.GraphNodeResponse{ID: artifactID, Kind: "artifact", Artifact: &value})
			addEdge(taskID, artifactID, "produced")
			for _, file := range artifact.Files {
				fileID := "file:" + artifact.ID + ":" + file.ID
				value := models.GraphFileResponse{
					ArtifactID: artifact.ID, ID: file.ID, Name: file.Name,
					MediaType: file.MediaType, Size: file.Size, SHA256: file.SHA256,
				}
				addNode(models.GraphNodeResponse{ID: fileID, Kind: "file", File: &value})
				addEdge(artifactID, fileID, "contains")
			}
		}
	}
	return response
}

func toGraphTaskResponse(task operations.Task) models.GraphTaskResponse {
	response := models.GraphTaskResponse{
		ID: task.ID, ParentTaskID: task.ParentTaskID, ActionName: task.ActionName,
		AgentID: task.AgentID, CommandID: task.CommandID, Status: string(task.Status), Error: task.Error,
		FinishedAt: cloneTime(task.FinishedAt),
	}
	if !task.StartedAt.IsZero() {
		response.StartedAt = cloneTime(&task.StartedAt)
	}
	return response
}
