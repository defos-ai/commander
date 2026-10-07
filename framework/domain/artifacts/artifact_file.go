package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/id"
)

type ArtifactFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

func (f ArtifactFile) Validate() error {
	if !id.Valid(f.ID) {
		return errors.New("artifact file ID must be a canonical UUID")
	}
	if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.MediaType) == "" || !utf8.ValidString(f.Name) || !utf8.ValidString(f.MediaType) {
		return errors.New("artifact file name and media type must contain nonblank UTF-8 text")
	}
	if f.Size < 0 {
		return errors.New("artifact file size must not be negative")
	}
	digest, err := hex.DecodeString(f.SHA256)
	if err != nil || len(digest) != sha256.Size || f.SHA256 != strings.ToLower(f.SHA256) {
		return errors.New("artifact file SHA256 must contain 64 lowercase hexadecimal characters")
	}
	return nil
}

// FileSource is local input to storage, never part of an agent message.
// Exactly one of Data or Path must be supplied; empty non-nil Data is valid.
type FileSource struct {
	Name      string
	MediaType string
	Data      []byte `json:"-"`
	Path      string `json:"-"`
}

func (f FileSource) Validate() error {
	if strings.TrimSpace(f.Name) == "" || !utf8.ValidString(f.Name) || !utf8.ValidString(f.MediaType) {
		return errors.New("artifact file name and media type must contain valid UTF-8")
	}
	if f.MediaType != "" && strings.TrimSpace(f.MediaType) == "" {
		return errors.New("artifact file media type must not be blank")
	}
	if (f.Data != nil) == (f.Path != "") {
		return errors.New("artifact file requires exactly one source: data or path")
	}
	return nil
}
