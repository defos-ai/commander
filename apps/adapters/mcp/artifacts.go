package missionsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"

	"commander/framework/artifact"
	"commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addArtifactResources(server *mcp.Server, config Config) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: artifactPrefix + "{id}", Name: "mission_artifact", MIMEType: "application/json",
		Description: "Read artifact metadata and its file descriptors. Read individual files using the artifact-file resource.",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		artifactID, ok := strings.CutPrefix(uri, artifactPrefix)
		if !ok || !id.Valid(artifactID) {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		value, err := config.ArtifactStore.Get(ctx, artifactID)
		if err != nil {
			return nil, artifactResourceError(uri, err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode artifact: %w", err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}}, nil
	})
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: artifactPrefix + "{id}/files/{file_id}", Name: "mission_artifact_file",
		Description: "Read a file belonging to a stored artifact. IDs identify records, never filesystem paths.",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		path, ok := strings.CutPrefix(uri, artifactPrefix)
		artifactID, fileID, hasFile := strings.Cut(path, "/files/")
		if !ok || !hasFile || !id.Valid(artifactID) || !id.Valid(fileID) {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		value, err := config.ArtifactStore.Get(ctx, artifactID)
		if err != nil {
			return nil, artifactResourceError(uri, err)
		}
		for _, file := range value.Files {
			if file.ID != fileID {
				continue
			}
			data, err := config.ArtifactFileStore.Get(ctx, value.ID, file)
			if err != nil {
				return nil, artifactResourceError(uri, err)
			}
			content := &mcp.ResourceContents{URI: uri, MIMEType: file.MediaType}
			mediaType, _, _ := mime.ParseMediaType(file.MediaType)
			if len(data) > 0 && utf8.Valid(data) && (strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) {
				content.Text = string(data)
			} else {
				// Text is omitted when empty; an explicit blob preserves an empty file.
				content.Blob = append([]byte{}, data...)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{content}}, nil
		}
		return nil, mcp.ResourceNotFoundError(uri)
	})
}

func artifactResourceError(uri string, err error) error {
	if errors.Is(err, artifact.ErrUnknownArtifact) {
		return mcp.ResourceNotFoundError(uri)
	}
	return fmt.Errorf("read artifact resource: %w", err)
}

func artifactLinks(value artifact.Artifact) []mcp.Content {
	links := make([]mcp.Content, 0, len(value.Files))
	for _, file := range value.Files {
		links = append(links, &mcp.ResourceLink{URI: artifactPrefix + value.ID + "/files/" + file.ID, Name: file.Name, MIMEType: file.MediaType, Size: &file.Size})
	}
	return links
}
