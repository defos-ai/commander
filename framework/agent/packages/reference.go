// Package packages defines agent package references and catalog metadata.
package packages

import (
	"errors"
	"regexp"
	"strings"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
var moduleSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Reference struct {
	Owner      string
	Repository string
	ModulePath string
	Version    string
}

// ParseReference accepts an exact GitHub release, without URL parameters.
func ParseReference(value string) (Reference, error) {
	return parseReference(value, false)
}

// ParseSelector also accepts @stable for a package whose release is selected later.
func ParseSelector(value string) (Reference, error) {
	return parseReference(value, true)
}

func parseReference(value string, allowStable bool) (Reference, error) {
	path, version, ok := strings.Cut(strings.TrimPrefix(value, "github.com/"), "@")
	owner, remainder, _ := strings.Cut(path, "/")
	name, modulePath, hasModule := strings.Cut(remainder, "/")
	validVersion := manifest.ValidVersion(version) || allowStable && version == "stable"
	if !strings.HasPrefix(value, "github.com/") || !ok || !repositoryPattern.MatchString(owner+"/"+name) || !validVersion {
		if allowStable {
			return Reference{}, errors.New("agent package must be github.com/owner/repository[/module/path]@vMAJOR.MINOR.PATCH with a nonzero stable version, or @stable")
		}
		return Reference{}, errors.New("agent package must be github.com/owner/repository[/module/path]@vMAJOR.MINOR.PATCH with a nonzero stable version")
	}
	if hasModule {
		for _, segment := range strings.Split(modulePath, "/") {
			if !moduleSegmentPattern.MatchString(segment) || strings.Contains(segment, "..") || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, ".lock") {
				return Reference{}, errors.New("agent package module path must contain safe nonempty release tag components")
			}
		}
	}
	return Reference{Owner: owner, Repository: name, ModulePath: modulePath, Version: version}, nil
}

func (r Reference) String() string {
	path := "github.com/" + r.Owner + "/" + r.Repository
	if r.ModulePath != "" {
		path += "/" + r.ModulePath
	}
	return path + "@" + r.Version
}

func (r Reference) ReleaseTag() string {
	if r.ModulePath != "" {
		return r.ModulePath + "/" + r.Version
	}
	return r.Version
}
