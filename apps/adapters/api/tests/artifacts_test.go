package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestArtifactDownloadsPreserveScreenshotBytesAndOwnership(t *testing.T) {
	a := connect(t)
	url := website(t)
	left, right := a.start(url), a.start(url+"/about")
	a.task(left.ID, "capture-page", url)
	a.task(right.ID, "capture-page", url+"/about")
	var previous *models.ArtifactResponse
	for _, op := range []models.OperationResponse{left, right} {
		finished := a.wait(op.ID, tasksSettled)
		task := finished.Tasks[0]
		if task.Status != "succeeded" || task.Result == nil || len(task.Result.Artifacts) != 1 {
			t.Fatalf("browser failed to produce screenshots: %+v", task)
		}
		artifact := task.Result.Artifacts[0]
		base := "/api/v1/artifacts/" + artifact.ID
		metadata := a.request(http.MethodGet, base, nil)
		saved := decode[models.ArtifactResponse](t, metadata, http.StatusOK)
		if !reflect.DeepEqual(saved, artifact) || saved.AgentID != task.AgentID || saved.CommandID != task.CommandID || len(saved.Files) != 2 {
			t.Fatalf("artifact ownership/grouping differs: %+v", saved)
		}
		assertSchema(t, a, "Artifact", metadata)
		assertHeaders(t, metadata)
		for _, file := range saved.Files {
			path := base + "/files/" + file.ID
			result := a.request(http.MethodGet, path, nil)
			if result.status != http.StatusOK {
				t.Fatalf("download failed: %+v", result)
			}
			digest := sha256.Sum256([]byte(result.body))
			if int64(len(result.body)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 || result.header.Get("Content-Length") != strconv.FormatInt(file.Size, 10) || result.header.Get("Content-Type") != file.MediaType {
				t.Fatalf("download differs from saved descriptor: %+v headers=%v", file, result.header)
			}
			kind, params, err := mime.ParseMediaType(result.header.Get("Content-Disposition"))
			if err != nil || kind != "attachment" || params["filename"] != file.Name {
				t.Fatalf("download filename: %q: %v", result.header.Get("Content-Disposition"), err)
			}
			image, err := png.DecodeConfig(bytes.NewReader([]byte(result.body)))
			if err != nil || image.Width <= 0 || image.Height <= 0 {
				t.Fatalf("artifact is not a screenshot PNG: %+v: %v", image, err)
			}
			head := a.request(http.MethodHead, path, nil)
			if head.status != http.StatusOK || head.body != "" || head.header.Get("Content-Length") != result.header.Get("Content-Length") || head.header.Get("Content-Disposition") != result.header.Get("Content-Disposition") {
				t.Fatalf("HEAD differs from GET: %+v", head)
			}
			assertHeaders(t, result)
			if repeated := a.request(http.MethodGet, path, nil); repeated.body != result.body {
				t.Fatal("repeated download changed bytes")
			}
		}
		if previous != nil {
			assertError(t, a.request(http.MethodGet, base+"/files/"+previous.Files[0].ID, nil), http.StatusNotFound, "artifact_file_not_found")
		}
		assertError(t, a.request(http.MethodGet, base+"/files/"+absentID, nil), http.StatusNotFound, "artifact_file_not_found")
		previous = &saved
		a.complete(op.ID, url, "Reviewed screenshot evidence for "+op.ID)
	}
	for _, invalid := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000ABCDEF"} {
		assertError(t, a.request(http.MethodGet, "/api/v1/artifacts/"+invalid, nil), http.StatusBadRequest, "invalid_artifact_id")
		assertError(t, a.request(http.MethodGet, "/api/v1/artifacts/"+previous.ID+"/files/"+invalid, nil), http.StatusBadRequest, "invalid_file_id")
	}
}
