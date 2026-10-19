// Package packages resolves versioned agent packages without executing them.
package packages

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Reference struct {
	Owner      string
	Repository string
	Version    string
}

// ParseReference accepts an exact public GitHub release, without URL parameters.
func ParseReference(value string) (Reference, error) {
	repository, version, ok := strings.Cut(strings.TrimPrefix(value, "github.com/"), "@")
	if !strings.HasPrefix(value, "github.com/") || !ok || !repositoryPattern.MatchString(repository) || !manifest.ValidVersion(version) {
		return Reference{}, errors.New("agent package must be github.com/owner/repository@vMAJOR.MINOR.PATCH with a nonzero stable version")
	}
	owner, name, _ := strings.Cut(repository, "/")
	return Reference{Owner: owner, Repository: name, Version: version}, nil
}

func (r Reference) String() string {
	return "github.com/" + r.Owner + "/" + r.Repository + "@" + r.Version
}

func (r Reference) assetURL(asset string) string {
	return "https://github.com/" + r.Owner + "/" + r.Repository + "/releases/download/" + r.Version + "/" + url.PathEscape(asset)
}
