package s3_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
	files3 "github.com/defos-ai/commander/providers/s3/artifacts"
	"github.com/defos-ai/commander/providers/s3/tests/fixtures"
)

func TestArtifactFileSourcesAndIntegrity(t *testing.T) {
	f := fixtures.OpenStore(t)
	files, err := files3.NewArtifactFileS3Store(f.Storage)
	if err != nil {
		t.Fatal(err)
	}
	artifactID := id.ArtifactID()
	payload := []byte("downloaded content")
	path := filepath.Join(t.TempDir(), "download.bin")
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		source artifacts.FileSource
		data   []byte
	}{
		{"bytes", artifacts.FileSource{Name: "../../display.png", MediaType: "image/png", Data: payload}, payload},
		{"path", artifacts.FileSource{Name: "download.bin", Path: path}, payload},
		{"empty", artifacts.FileSource{Name: "empty.bin", Data: []byte{}}, []byte{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := files.Put(t.Context(), artifactID, test.source)
			if err != nil {
				t.Fatal(err)
			}
			data, err := files.Get(t.Context(), artifactID, file)
			if err != nil || !bytes.Equal(data, test.data) {
				t.Fatalf("file roundtrip: %q, %v", data, err)
			}
			digest := sha256.Sum256(test.data)
			if err := file.Validate(); err != nil || file.Name != test.source.Name || file.Size != int64(len(test.data)) || file.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("file metadata: %+v, %v", file, err)
			}
			if test.source.MediaType == "" && file.MediaType != "application/octet-stream" {
				t.Fatalf("default media type: %s", file.MediaType)
			}
			if _, err := files.Get(t.Context(), id.ArtifactID(), file); !errors.Is(err, artifacts.ErrUnknownArtifact) {
				t.Fatalf("another artifact accessed this file: %v", err)
			}
			wrongSize := file
			wrongSize.Size++
			if _, err := files.Get(t.Context(), artifactID, wrongSize); err == nil {
				t.Fatal("incorrect length passed verification")
			}
			wrongHash := file
			wrongHash.SHA256 = strings.Repeat("0", 64)
			if _, err := files.Get(t.Context(), artifactID, wrongHash); err == nil {
				t.Fatal("incorrect hash passed verification")
			}
			key := f.Prefix + "artifacts/" + artifactID + "/" + file.ID
			_, err = f.Client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String(f.Bucket), Key: aws.String(key), Body: strings.NewReader("tampered")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := files.Get(t.Context(), artifactID, file); err == nil {
				t.Fatal("tampered object passed verification")
			}
		})
	}
}

func TestArtifactFileRejectsInvalidAndCancelledRequests(t *testing.T) {
	f := fixtures.OpenStore(t)
	files, err := files3.NewArtifactFileS3Store(f.Storage)
	if err != nil {
		t.Fatal(err)
	}
	artifactID := id.ArtifactID()
	root := t.TempDir()
	for _, source := range []artifacts.FileSource{
		{Name: "no source"},
		{Name: "two sources", Data: []byte{}, Path: root},
		{Name: "directory", Path: root},
		{Name: "missing", Path: filepath.Join(root, "missing")},
	} {
		if _, err := files.Put(t.Context(), artifactID, source); err == nil {
			t.Fatalf("invalid source accepted: %+v", source)
		}
	}
	source := artifacts.FileSource{Name: "valid", Data: []byte("data")}
	if _, err := files.Put(t.Context(), "../outside", source); err == nil {
		t.Fatal("invalid artifact ID accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := files.Put(cancelled, artifactID, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v", err)
	}
	file := artifacts.ArtifactFile{ID: id.ArtifactFileID(), Name: "missing", MediaType: "application/octet-stream", Size: 0, SHA256: strings.Repeat("0", 64)}
	if _, err := files.Get(cancelled, artifactID, file); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := files.Get(t.Context(), artifactID, file); !errors.Is(err, artifacts.ErrUnknownArtifact) {
		t.Fatalf("missing file: %v", err)
	}
	page, err := f.Client.ListObjectsV2(t.Context(), &awss3.ListObjectsV2Input{Bucket: aws.String(f.Bucket), Prefix: aws.String(f.Prefix)})
	if err != nil || len(page.Contents) != 0 {
		t.Fatalf("invalid/cancelled writes created objects: %+v, %v", page, err)
	}
}
