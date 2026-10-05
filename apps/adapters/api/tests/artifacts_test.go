package api_test

import (
	"context"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"commander/adapters/api/endpoints"
	"commander/framework/domain/artifacts"
	fsartifacts "commander/providers/fs/artifacts"
)

func TestArtifactsReturnSavedMetadataAndFilesWithoutController(t *testing.T) {
	fixture := newFixture(t)
	cases := []struct {
		name, mediaType, contentType string
		data                         []byte
	}{
		{"résumé.txt", "Text/Plain; Charset=UTF-8", "text/plain; charset=UTF-8", []byte("Saved résumé\n")},
		{"../../review\";\r\nX-Injected: yes\\notes.bin", "application/octet-stream", "application/octet-stream", []byte{0, 255, 1, 128, '\n'}},
		{"empty.bin", "", "application/octet-stream", []byte{}},
		{"invalid-mime.txt", "text/plain\r\nX-Injected: yes", "application/octet-stream", []byte("Untrusted media type")},
	}
	sources := make([]artifacts.FileSource, len(cases))
	for i, tc := range cases {
		sources[i] = artifacts.FileSource{Name: tc.name, MediaType: tc.mediaType, Data: tc.data}
	}
	artifact := saveArtifact(t, fixture, 1, sources...)
	before := artifactStorageFiles(t, fixture)
	// No controller or operation is created: both routes read durable stores.
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/artifacts/" + artifact.ID
	metadata := server.request(t, http.MethodGet, base, authenticate)
	if got := decodeResponse[artifacts.Artifact](t, metadata); !reflect.DeepEqual(got, artifact) {
		t.Fatalf("metadata differs from the full saved artifact: got %+v, want %+v", got, artifact)
	}
	assertSecurityHeaders(t, metadata, false)
	head := server.request(t, http.MethodHead, base, authenticate)
	if head.status != http.StatusOK || head.body != "" || head.header.Get("Content-Type") != "application/json" {
		t.Fatalf("HEAD metadata: status=%d headers=%v body=%q", head.status, head.header, head.body)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := base + "/files/" + artifact.Files[i].ID
			result := server.request(t, http.MethodGet, path, authenticate)
			if result.status != http.StatusOK || result.body != string(tc.data) {
				t.Fatalf("download changed bytes: status=%d body=%q, want %q", result.status, result.body, tc.data)
			}
			assertSecurityHeaders(t, result, false)
			if got := result.header.Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type=%q, want %q", got, tc.contentType)
			}
			if got := result.header.Get("Content-Length"); got != strconv.Itoa(len(tc.data)) {
				t.Errorf("Content-Length=%q, want %d", got, len(tc.data))
			}
			disposition, params, err := mime.ParseMediaType(result.header.Get("Content-Disposition"))
			filename := params["filename"]
			if err != nil || disposition != "attachment" || filename == "" || strings.ContainsAny(filename, "/\\") || strings.IndexFunc(filename, unicode.IsControl) >= 0 {
				t.Fatalf("unsafe attachment filename: header=%q parsed=%q error=%v", result.header.Get("Content-Disposition"), filename, err)
			}
			if i == 0 && filename != tc.name {
				t.Errorf("Unicode filename changed: got %q, want %q", filename, tc.name)
			}
			if result.header.Get("X-Injected") != "" {
				t.Fatal("stored filename or media type injected a response header")
			}
			head := server.request(t, http.MethodHead, path, authenticate)
			if head.status != http.StatusOK || head.body != "" {
				t.Fatalf("HEAD download: status=%d body=%q", head.status, head.body)
			}
			for _, name := range []string{"Content-Type", "Content-Length", "Content-Disposition"} {
				if head.header.Get(name) != result.header.Get(name) {
					t.Errorf("HEAD %s=%q, GET=%q", name, head.header.Get(name), result.header.Get(name))
				}
			}
			assertSecurityHeaders(t, head, false)
		})
	}
	if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
		t.Fatal("artifact reads changed stored metadata or file bytes")
	}
}

func TestArtifactQueriesValidateIDsAndFileMembership(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	before := artifactStorageFiles(t, fixture)
	missing := "/api/v1/artifacts/" + recordID(999)
	for _, path := range []string{missing, missing + "/files/" + recordID(998)} {
		assertArtifactError(t, server.request(t, http.MethodGet, path, authenticate), http.StatusNotFound, "artifact_not_found")
	}
	if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
		t.Fatal("missing artifact reads created storage")
	}
	artifact := saveArtifact(t, fixture, 1, artifacts.FileSource{Name: "saved.txt", Data: []byte("Saved bytes")})
	other := saveArtifact(t, fixture, 2, artifacts.FileSource{Name: "other.txt", Data: []byte("Other artifact's private bytes")})
	base := "/api/v1/artifacts/" + artifact.ID
	before = artifactStorageFiles(t, fixture)
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(artifact.ID, "-", ""), "%20" + artifact.ID} {
		for _, suffix := range []string{"", "/files/" + artifact.Files[0].ID} {
			assertArtifactError(t, server.request(t, http.MethodGet, "/api/v1/artifacts/"+invalid+suffix, authenticate), http.StatusBadRequest, "invalid_artifact_id")
		}
		assertArtifactError(t, server.request(t, http.MethodGet, base+"/files/"+invalid, authenticate), http.StatusBadRequest, "invalid_file_id")
	}
	for _, fileID := range []string{recordID(997), other.Files[0].ID} {
		assertArtifactError(t, server.request(t, http.MethodGet, base+"/files/"+fileID, authenticate), http.StatusNotFound, "artifact_file_not_found")
	}
	if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
		t.Fatal("invalid artifact queries changed storage")
	}
}

func TestArtifactQueriesRejectMissingOrCorruptStorageBeforeDownloadHeaders(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	for i, name := range []string{"missing-file", "corrupt-file", "corrupt-metadata"} {
		t.Run(name, func(t *testing.T) {
			artifact := saveArtifact(t, fixture, i+1, artifacts.FileSource{Name: "private.txt", MediaType: "text/plain", Data: []byte("trusted bytes")})
			path := filepath.Join(fixture.root, "storage", "artifacts", artifact.ID, artifact.Files[0].ID)
			status, code := http.StatusInternalServerError, "internal_error"
			var err error
			switch name {
			case "missing-file":
				err = os.Remove(path)
				status, code = http.StatusNotFound, "artifact_file_not_found"
			case "corrupt-file":
				// Same length: verifying only the byte count cannot detect this change.
				err = os.WriteFile(path, []byte("altered bytes"), 0600)
			case "corrupt-metadata":
				err = os.WriteFile(filepath.Join(fixture.root, "database", "artifacts", artifact.ID+".json"), []byte("private-metadata-not-json"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := artifactStorageFiles(t, fixture)
			base := "/api/v1/artifacts/" + artifact.ID
			assertArtifactError(t, server.request(t, http.MethodGet, base+"/files/"+artifact.Files[0].ID, authenticate), status, code)
			if name == "corrupt-metadata" {
				assertArtifactError(t, server.request(t, http.MethodGet, base, authenticate), status, code)
			} else if got := decodeResponse[artifacts.Artifact](t, server.request(t, http.MethodGet, base, authenticate)); !reflect.DeepEqual(got, artifact) {
				t.Fatalf("file failure changed independently stored metadata: %+v", got)
			}
			if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
				t.Fatal("unsuccessful reads repaired or rewrote artifact storage")
			}
		})
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))
}

func TestArtifactQueriesKeepAuthenticationOriginAndMethodProtections(t *testing.T) {
	fixture := newFixture(t)
	artifact := saveArtifact(t, fixture, 1, artifacts.FileSource{Name: "private.txt", Data: []byte("private bytes")})
	server := startAPI(t, fixture.localArgs())
	before := artifactStorageFiles(t, fixture)
	base := "/api/v1/artifacts/" + artifact.ID
	for _, path := range []string{base, base + "/files/" + artifact.Files[0].ID} {
		assertArtifactError(t, server.request(t, http.MethodGet, path), http.StatusUnauthorized, "authentication_required")
		assertArtifactError(t, server.request(t, http.MethodGet, path, authenticate, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }), http.StatusForbidden, "cross_origin_request")
		result := server.request(t, http.MethodPost, path, authenticate)
		if result.status != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s returned %d: %s", path, result.status, result.body)
		}
		assertSecurityHeaders(t, result, false)
	}
	if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
		t.Fatal("rejected artifact requests changed storage")
	}
}

func TestArtifactQueriesMapExpiredStoreReadsToTimeout(t *testing.T) {
	fixture := newFixture(t)
	artifact := saveArtifact(t, fixture, 1, artifacts.FileSource{Name: "saved.txt", Data: []byte("Saved bytes")})
	metadata, files := artifactStores(t, fixture)
	mux := http.NewServeMux()
	endpoints.RegisterArtifacts(mux, metadata, files)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	base := "/api/v1/artifacts/" + artifact.ID
	before := artifactStorageFiles(t, fixture)
	for _, path := range []string{base, base + "/files/" + artifact.Files[0].ID} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		result := response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}
		assertAPIError(t, result, http.StatusGatewayTimeout, "request_timeout")
		if result.header.Get("Content-Disposition") != "" || result.header.Get("Content-Type") != "application/json" {
			t.Fatalf("timeout committed download headers: %v", result.header)
		}
	}
	if !reflect.DeepEqual(before, artifactStorageFiles(t, fixture)) {
		t.Fatal("expired reads changed artifact storage")
	}
}

func artifactStores(t *testing.T, fixture fixture) (*fsartifacts.ArtifactFsStore, *fsartifacts.ArtifactFileFsStore) {
	t.Helper()
	metadata, err := fsartifacts.NewArtifactFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	return metadata, files
}

func saveArtifact(t *testing.T, fixture fixture, number int, sources ...artifacts.FileSource) artifacts.Artifact {
	t.Helper()
	metadata, files := artifactStores(t, fixture)
	value := artifacts.Artifact{ID: recordID(number), Name: "Saved evidence", AgentID: "review-agent-1234abcd", ActionName: "review", CommandID: recordID(number + 100), CreatedAt: operationRecord(1).CreatedAt, Files: []artifacts.ArtifactFile{}}
	for _, source := range sources {
		file, err := files.Put(t.Context(), value.ID, source)
		if err != nil {
			t.Fatal(err)
		}
		value.Files = append(value.Files, file)
	}
	if err := metadata.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertArtifactError(t *testing.T, result response, status int, code string) {
	t.Helper()
	assertAPIError(t, result, status, code)
	assertSecurityHeaders(t, result, false)
	if result.header.Get("Content-Disposition") != "" || result.header.Get("Content-Type") != "application/json" {
		t.Fatalf("error response committed download headers: %v", result.header)
	}
}

func artifactStorageFiles(t *testing.T, fixture fixture) map[string]string {
	t.Helper()
	files := make(map[string]string)
	for _, area := range []string{"database", "storage"} {
		err := filepath.WalkDir(filepath.Join(fixture.root, area, "artifacts"), func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				files[path+string(filepath.Separator)] = ""
				return nil
			}
			data, err := os.ReadFile(path)
			files[path] = string(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}
