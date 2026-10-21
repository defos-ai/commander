package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/id"
)

type PackageMetadata struct {
	AgentType  string
	ModulePath string
	Binary     string
}

const releaseCheckVersion = "v0.1.0"
const releaseCheckRepository = "release-check/generated-agent"

// ValidateRelease exercises the generated release helper and manifest against
// synthetic assets. These checks complement local tests; they do not run CI or
// establish that the eventual binaries or Docker image have been published.
func ValidateRelease(ctx context.Context, goTool, source, scratch string, env []string) (PackageMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return PackageMetadata{}, err
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return PackageMetadata{}, errors.New("release validation cannot open the generated source")
	}
	defer root.Close()
	var agentSource []byte
	for _, name := range []string{
		"agent.go", "go.mod", "LICENSE", "README.md", "AGENTS.md", "agent.yaml.tmpl",
		"docker/Dockerfile", ".github/scripts/release.go",
		".github/workflows/make-release.yml", ".github/workflows/test.yml",
		".github/workflows/build-binaries.yml", ".github/workflows/push.yml", ".github/workflows/push-stable.yml",
	} {
		if err := ctx.Err(); err != nil {
			return PackageMetadata{}, err
		}
		data, err := readReleaseFile(root, name, 1<<20)
		if err != nil {
			return PackageMetadata{}, fmt.Errorf("release validation requires a nonempty regular %s of at most 1 MiB", name)
		}
		if name == "agent.go" {
			agentSource = data
		}
	}
	agentType, err := releaseAgentType(agentSource)
	if err != nil {
		return PackageMetadata{}, err
	}
	module, err := releaseModule(ctx, goTool, source, env)
	if err != nil {
		return PackageMetadata{}, err
	}
	output, err := CommandOutput(ctx, "Release metadata", goTool, source, env,
		"run", "./.github/scripts/release.go", "metadata", "--version", releaseCheckVersion, "--repository", releaseCheckRepository)
	if err != nil {
		return PackageMetadata{}, err
	}
	var metadata struct {
		Version string `json:"version"`
		Type    string `json:"type"`
		Binary  string `json:"binary"`
		Image   string `json:"image"`
	}
	if err := decodeReleaseJSON(output, &metadata, true); err != nil {
		return PackageMetadata{}, errors.New("release helper metadata must contain only version, type, binary and image")
	}
	binary := "commander-" + agentType
	image := "ghcr.io/release-check/" + binary
	if metadata.Version != releaseCheckVersion || metadata.Type != agentType || metadata.Binary != binary || metadata.Image != image || !filepath.IsLocal(metadata.Binary) || filepath.Base(metadata.Binary) != metadata.Binary {
		return PackageMetadata{}, errors.New("release helper metadata must match AgentType, commander-<type> binary and the requested repository owner")
	}
	if err := validateReleaseManifest(ctx, goTool, source, scratch, env, agentType, binary, image); err != nil {
		return PackageMetadata{}, err
	}
	return PackageMetadata{AgentType: agentType, ModulePath: module, Binary: binary}, nil
}

func readReleaseFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > limit {
		return nil, errors.New("unsafe or missing release file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("unsafe release file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("invalid release file size")
	}
	return data, nil
}

func releaseAgentType(data []byte) (string, error) {
	invalid := errors.New("agent.go must declare a distinct valid AgentType as one string literal; replace example-agent")
	file, err := parser.ParseFile(token.NewFileSet(), "agent.go", data, 0)
	if err != nil {
		return "", invalid
	}
	var value string
	for _, declaration := range file.Decls {
		decl, ok := declaration.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			continue
		}
		for _, specification := range decl.Specs {
			spec := specification.(*ast.ValueSpec)
			for index, name := range spec.Names {
				if name.Name != "AgentType" {
					continue
				}
				if value != "" || index >= len(spec.Values) {
					return "", invalid
				}
				literal, ok := spec.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return "", invalid
				}
				value, err = strconv.Unquote(literal.Value)
				if err != nil || !id.ValidType(value) || value == "example-agent" {
					return "", invalid
				}
			}
		}
	}
	if _, err := id.AgentIDFromSuffix(value, "00000000"); err != nil {
		return "", invalid
	}
	return value, nil
}

func releaseModule(ctx context.Context, goTool, source string, env []string) (string, error) {
	output, err := CommandOutput(ctx, "Go module metadata", goTool, source, env, "mod", "edit", "-json")
	if err != nil {
		return "", err
	}
	var module struct {
		Module  *struct{ Path string }
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	if err := decodeReleaseJSON(output, &module, false); err != nil || module.Module == nil || strings.TrimSpace(module.Module.Path) == "" || module.Module.Path == "github.com/defos-ai/commander-example-agent" {
		return "", errors.New("go.mod must declare the generated agent's own module path instead of commander-example-agent")
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version == "" {
			return "", errors.New("go.mod contains a local replace directive; use published module versions before release")
		}
	}
	return module.Module.Path, nil
}

func decodeReleaseJSON(data []byte, out any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("expected exactly one JSON object")
	}
	return nil
}

func validateReleaseManifest(ctx context.Context, goTool, source, scratch string, env []string, agentType, binary, image string) error {
	// Keep placeholders outside the exported source and remove them after this
	// check. Their checksums test packaging, never represent native executables.
	assets, err := os.MkdirTemp(scratch, "release-check-")
	if err != nil {
		return errors.New("prepare release validation assets failed")
	}
	defer os.RemoveAll(assets)
	root, err := os.OpenRoot(assets)
	if err != nil {
		return errors.New("prepare release validation assets failed")
	}
	defer root.Close()
	expected := make(map[string]manifest.NativeAsset, 6)
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			platform := goos + "/" + arch
			name := binary + "_" + goos + "_" + arch
			if goos == "windows" {
				name += ".exe"
			}
			data := []byte("release readiness fixture for " + platform + "\n")
			file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return errors.New("prepare release validation assets failed")
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				return errors.New("write release validation assets failed")
			}
			expected[platform] = manifest.NativeAsset{Asset: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
		}
	}
	digest := "sha256:" + strings.Repeat("0", 64)
	if err := RunCommand(ctx, "Release manifest", goTool, source, env, nil,
		"run", "./.github/scripts/release.go", "manifest", "--version", releaseCheckVersion,
		"--repository", releaseCheckRepository, "--digest", digest, "--assets", assets); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := readReleaseFile(root, "agent.yaml", manifest.MaxBytes)
	if err != nil {
		return errors.New("release helper must generate a bounded regular agent.yaml in its asset directory")
	}
	result, err := manifest.Parse(data, releaseCheckVersion)
	if err != nil {
		return errors.New("release helper generated an invalid agent.yaml; preserve the agent package manifest contract")
	}
	if result.Type != agentType || result.Docker == nil || result.Docker.Image != image+"@"+digest || len(result.Native) != len(expected) {
		return errors.New("agent.yaml must match AgentType, the Docker digest and all six native release platforms")
	}
	for platform, asset := range expected {
		if result.Native[platform] != asset {
			return errors.New("agent.yaml must use the expected native asset names and their SHA-256 checksums")
		}
	}
	return nil
}
