package endpoints

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode"

	"commander/framework/domain/artifacts"
	"commander/framework/id"
)

func RegisterArtifacts(mux *http.ServeMux, store artifacts.ArtifactStore, files artifacts.ArtifactFileStore) {
	mux.HandleFunc("GET /api/v1/artifacts/{artifact_id}", artifactRequestHandler(func(w http.ResponseWriter, r *http.Request) {
		artifact, err := store.Get(r.Context(), r.PathValue("artifact_id"))
		WriteQueryResult(w, artifact, err)
	}))
	mux.HandleFunc("GET /api/v1/artifacts/{artifact_id}/files/{file_id}", artifactRequestHandler(func(w http.ResponseWriter, r *http.Request) {
		artifact, err := store.Get(r.Context(), r.PathValue("artifact_id"))
		if err != nil {
			WriteQueryResult(w, nil, err)
			return
		}
		for _, file := range artifact.Files {
			if file.ID != r.PathValue("file_id") {
				continue
			}
			data, err := files.Get(r.Context(), artifact.ID, file)
			if errors.Is(err, artifacts.ErrUnknownArtifact) || errors.Is(err, fs.ErrNotExist) {
				WriteError(w, http.StatusNotFound, "artifact_file_not_found")
				return
			}
			if err != nil {
				WriteQueryResult(w, nil, err)
				return
			}
			writeArtifactFile(w, r, file, data)
			return
		}
		WriteError(w, http.StatusNotFound, "artifact_file_not_found")
	}))
}

func artifactRequestHandler(query http.HandlerFunc) http.HandlerFunc {
	return RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"artifact_id", "file_id"} {
			if value := r.PathValue(name); value != "" && !id.Valid(value) {
				WriteError(w, http.StatusBadRequest, "invalid_"+name)
				return
			}
		}
		query(w, r)
	})
}

func writeArtifactFile(w http.ResponseWriter, r *http.Request, file artifacts.ArtifactFile, data []byte) {
	mediaType, parameters, err := mime.ParseMediaType(file.MediaType)
	contentType := mime.FormatMediaType(mediaType, parameters)
	if err != nil || !strings.Contains(mediaType, "/") || contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifactFileName(file)}))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func artifactFileName(file artifacts.ArtifactFile) string {
	name := path.Base(strings.ReplaceAll(file.Name, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." || name == ".." || name == "/" {
		return file.ID
	}
	return name
}
