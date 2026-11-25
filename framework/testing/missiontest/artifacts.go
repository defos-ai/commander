package missiontest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

func (d *Driver) Download(artifactID string, file artifacts.ArtifactFile) []byte {
	d.t.Helper()
	if !id.Valid(artifactID) || file.Validate() != nil {
		d.t.Fatal("missiontest: download requires valid artifact and file metadata")
	}
	if file.Size > d.maxArtifact {
		d.t.Fatal("missiontest: artifact exceeds the configured byte limit")
	}
	path := "/api/v1/artifacts/" + artifactID + "/files/" + file.ID
	raw, err := d.exchange(d.t.Context(), http.MethodGet, path, nil, http.StatusOK, file.Size)
	if err != nil {
		d.t.Fatalf("missiontest: artifact download failed: %v", err)
	}
	digest := sha256.Sum256(raw)
	if int64(len(raw)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
		d.t.Fatal("missiontest: artifact failed size or SHA-256 verification")
	}
	return raw
}
