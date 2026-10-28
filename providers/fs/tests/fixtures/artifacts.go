package fixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

func Artifact() artifacts.Artifact {
	digest := sha256.Sum256([]byte("data"))
	return artifacts.Artifact{ID: id.ArtifactID(), Name: "page screenshots", AgentID: "browser-agent-1234abcd",
		ActionName: "capture-page", CommandID: id.MessageID(), CreatedAt: time.Now().UTC(),
		Files: []artifacts.ArtifactFile{{ID: id.ArtifactFileID(), Name: "desktop.png", MediaType: "image/png", Size: 4, SHA256: hex.EncodeToString(digest[:])}}}
}
