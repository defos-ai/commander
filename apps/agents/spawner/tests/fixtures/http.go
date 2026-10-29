package fixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	PackageReference = "github.com/defos-ai/example-agent@v0.1.0"
	ReleaseURL       = "https://github.com/defos-ai/example-agent/releases/download/v0.1.0/"
	ModuleReference  = "github.com/defos-ai/example-agent/apps/agents/example@v0.1.0"
	ModuleReleaseURL = "https://github.com/defos-ai/example-agent/releases/download/apps%2Fagents%2Fexample%2Fv0.1.0/"
	BinaryAsset      = "example-agent_linux_amd64"
)

func Response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func PackageManifest(binary string) string {
	checksum := sha256.Sum256([]byte(binary))
	return fmt.Sprintf("schema_version: 1\ntype: example-agent\nversion: v0.1.0\nnative:\n  linux/amd64:\n    asset: %s\n    sha256: %q\n", BinaryAsset, hex.EncodeToString(checksum[:]))
}
