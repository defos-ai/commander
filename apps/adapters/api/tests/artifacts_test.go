package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestArtifactDownloadsPreserveBytesAndOwnership(t *testing.T) {
	a := fixtures.Connect(t)
	left, right := a.Start(fixtures.FixtureText), a.Start("Independent input")
	a.Task(left.ID, "write-summary", fixtures.SummaryInput())
	a.Task(right.ID, "write-summary", fixtures.SummaryInput())
	var previous *models.ArtifactResponse
	for _, op := range []models.OperationResponse{left, right} {
		finished := a.Wait(op.ID, fixtures.TasksSettled)
		task := finished.Tasks[0]
		if task.Status != "succeeded" || task.Result == nil || len(task.Result.Artifacts) != 1 {
			t.Fatalf("summary action did not publish an artifact: %+v", task)
		}
		artifact := task.Result.Artifacts[0]
		base := "/api/v1/artifacts/" + artifact.ID
		metadata := a.Request(http.MethodGet, base, nil)
		saved := fixtures.Decode[models.ArtifactResponse](t, metadata, http.StatusOK)
		if !reflect.DeepEqual(saved, artifact) || saved.AgentID != task.AgentID || saved.CommandID != task.CommandID || len(saved.Files) != 1 {
			t.Fatalf("artifact ownership/grouping differs: %+v", saved)
		}
		assertSchema(t, a, "Artifact", metadata)
		assertHeaders(t, metadata)
		for _, file := range saved.Files {
			path := base + "/files/" + file.ID
			result := a.Request(http.MethodGet, path, nil)
			if result.Status != http.StatusOK {
				t.Fatalf("download failed: %+v", result)
			}
			digest := sha256.Sum256([]byte(result.Body))
			if int64(len(result.Body)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 || result.Header.Get("Content-Length") != strconv.FormatInt(file.Size, 10) || result.Header.Get("Content-Type") != file.MediaType {
				t.Fatalf("download differs from saved descriptor: %+v headers=%v", file, result.Header)
			}
			kind, params, err := mime.ParseMediaType(result.Header.Get("Content-Disposition"))
			if err != nil || kind != "attachment" || params["filename"] != file.Name {
				t.Fatalf("download filename: %q: %v", result.Header.Get("Content-Disposition"), err)
			}
			if file.Name != "summary.md" || file.MediaType != "text/markdown" || !strings.HasPrefix(result.Body, "# Text summary\n") {
				t.Fatalf("download lost the agent's artifact descriptor or bytes: %+v", file)
			}
			head := a.Request(http.MethodHead, path, nil)
			if head.Status != http.StatusOK || head.Body != "" || head.Header.Get("Content-Length") != result.Header.Get("Content-Length") || head.Header.Get("Content-Disposition") != result.Header.Get("Content-Disposition") {
				t.Fatalf("HEAD differs from GET: %+v", head)
			}
			assertHeaders(t, result)
			if repeated := a.Request(http.MethodGet, path, nil); repeated.Body != result.Body {
				t.Fatal("repeated download changed bytes")
			}
		}
		if previous != nil {
			fixtures.AssertError(t, a.Request(http.MethodGet, base+"/files/"+previous.Files[0].ID, nil), http.StatusNotFound, "artifact_file_not_found")
		}
		fixtures.AssertError(t, a.Request(http.MethodGet, base+"/files/"+fixtures.AbsentID, nil), http.StatusNotFound, "artifact_file_not_found")
		previous = &saved
		a.Complete(op.ID, "Reviewed summary evidence for "+op.ID)
	}
	for _, invalid := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000ABCDEF"} {
		fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/artifacts/"+invalid, nil), http.StatusBadRequest, "invalid_artifact_id")
		fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/artifacts/"+previous.ID+"/files/"+invalid, nil), http.StatusBadRequest, "invalid_file_id")
	}
}
