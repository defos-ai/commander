package fixtures

import (
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

func Artifact() artifacts.Artifact {
	return artifacts.Artifact{ID: id.ArtifactID(), Name: "screenshots", AgentID: "browser-agent-1234abcd",
		ActionName: "capture-page", CommandID: id.MessageID(), CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC),
		Files: []artifacts.ArtifactFile{
			{ID: id.ArtifactFileID(), Name: "desktop.png", MediaType: "image/png", Size: 128, SHA256: strings.Repeat("a", 64)},
			{ID: id.ArtifactFileID(), Name: "mobile.png", MediaType: "image/png", Size: 64, SHA256: strings.Repeat("b", 64)},
		}}
}
