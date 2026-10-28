package packages

import (
	"errors"
	"net/url"
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

// ParseSelector accepts GitHub package references or HTTPS repository URLs.
// An omitted version means @stable; the spawner selects the exact release later.
func ParseSelector(value string) (Reference, error) {
	if strings.ContainsAny(value, " \t\r\n") {
		return Reference{}, errors.New("agent package must not contain whitespace")
	}
	isURL := strings.HasPrefix(value, "https://")
	explicitVersion := strings.Contains(value, "@")
	if isURL {
		target, err := url.Parse(value)
		if err != nil || target.Host != "github.com" || target.User != nil || target.Opaque != "" || target.RawQuery != "" || target.ForceQuery || strings.ContainsAny(value, "#%") {
			return Reference{}, errors.New("agent package URL must be HTTPS on github.com without credentials, query or fragment")
		}
		value = "github.com" + target.Path
	}
	if !strings.Contains(value, "@") {
		// A copied repository homepage URL commonly includes a trailing slash.
		if strings.HasSuffix(value, "/") && strings.Count(value, "/") == 3 {
			value = strings.TrimSuffix(value, "/")
		}
		value += "@stable"
	}
	ref, err := parseReference(value, true)
	if err != nil {
		return Reference{}, err
	}
	first, _, _ := strings.Cut(ref.ModulePath, "/")
	switch first {
	case "tree", "blob":
		if isURL || !explicitVersion {
			return Reference{}, errors.New("agent package must identify a repository or module, not a GitHub web page")
		}
	case "releases", "issues", "pull", "pulls", "actions", "commit", "commits", "compare", "archive", "tags":
		if isURL {
			return Reference{}, errors.New("agent package must identify a repository or module, not a GitHub web page")
		}
	}
	return ref, nil
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
