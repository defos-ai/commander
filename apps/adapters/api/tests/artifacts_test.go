package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	left, right := a.Start(fixtures.FixtureTitle), a.Start("Independent input")
	a.Task(left.ID, "write-hn-digest", fixtures.StoryBatch(fixtures.FixtureTitle))
	a.Task(right.ID, "write-hn-digest", fixtures.StoryBatch(fixtures.FixtureTitle))
	var previous *models.ArtifactResponse
	for _, op := range []models.OperationResponse{left, right} {
		finished := a.Wait(op.ID, fixtures.TasksSettled)
		task := finished.Tasks[0]
		if task.Status != "succeeded" || task.Result == nil || len(task.Result.Artifacts) != 1 {
			t.Fatalf("digest action did not publish an artifact: %+v", task)
		}
		artifact := task.Result.Artifacts[0]
		base := "/api/v1/artifacts/" + artifact.ID
		metadata := a.Request(http.MethodGet, base, nil)
		saved := fixtures.Decode[models.ArtifactResponse](t, metadata, http.StatusOK)
		if !reflect.DeepEqual(saved, artifact) || saved.AgentID != task.AgentID || saved.CommandID != task.CommandID || len(saved.Files) != 2 {
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
			switch file.Name {
			case "digest.md":
				if file.MediaType != "text/markdown" || !strings.HasPrefix(result.Body, "# Hacker News reading digest\n") || !strings.Contains(result.Body, fixtures.FixtureTitle) {
					t.Fatalf("download lost the digest descriptor or bytes: %+v", file)
				}
			case "stories.json":
				var batch fixtures.Object
				if file.MediaType != "application/json" || json.Unmarshal([]byte(result.Body), &batch) != nil {
					t.Fatalf("download lost the story batch descriptor or JSON bytes: %+v", file)
				}
				want, err := json.Marshal(fixtures.StoryBatch(fixtures.FixtureTitle))
				if err != nil {
					t.Fatal(err)
				}
				var expected fixtures.Object
				if json.Unmarshal(want, &expected) != nil || !reflect.DeepEqual(batch, expected) {
					t.Fatal("download changed the supplied story batch")
				}
			default:
				t.Fatalf("unexpected digest artifact file: %+v", file)
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
