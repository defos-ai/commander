// Package packages resolves versioned agent packages without executing them.
package packages

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionPattern    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

type Reference struct {
	Owner      string
	Repository string
	Version    string
}

// ParseReference accepts an exact public GitHub release, without URL parameters.
func ParseReference(value string) (Reference, error) {
	repository, version, ok := strings.Cut(strings.TrimPrefix(value, "github.com/"), "@")
	if !strings.HasPrefix(value, "github.com/") || !ok || !repositoryPattern.MatchString(repository) || !validVersion(version) {
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

func validVersion(value string) bool {
	return value != "v0.0.0" && versionPattern.MatchString(value)
}
