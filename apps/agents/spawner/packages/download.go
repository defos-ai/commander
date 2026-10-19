package packages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

const MaxDownloadBytes = 512 << 20

// DownloadNative creates a private child directory under directory. It returns
// an executable only after verifying all bytes; the caller owns successful output.
// Failed downloads remove only their own directory, leaving prior downloads intact.
func (r *Resolver) DownloadNative(ctx context.Context, pkg Package, goos, goarch, directory string) (path string, result error) {
	ref, err := agentpackages.ParseReference(pkg.Reference.String())
	if err != nil || ref != pkg.Reference {
		return "", errors.New("invalid package reference")
	}
	if pkg.Manifest.Version != ref.Version {
		return "", errors.New("package manifest version does not match the requested release")
	}
	asset, err := pkg.Manifest.NativeFor(goos, goarch)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	response, err := r.get(ctx, assetURL(ref, asset.Asset))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.ContentLength > MaxDownloadBytes {
		return "", errors.New("native agent exceeds the 512 MiB download limit")
	}
	private, err := os.MkdirTemp(directory, "agent-")
	if err != nil {
		return "", fmt.Errorf("create package directory: %w", err)
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, os.RemoveAll(private))
		}
	}()
	temporary := filepath.Join(private, ".download")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, MaxDownloadBytes+1))
	closeErr := file.Close()
	if err != nil {
		return "", downloadError(ctx, "save native agent")
	}
	if closeErr != nil {
		return "", fmt.Errorf("close native agent: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if size == 0 || size > MaxDownloadBytes {
		return "", errors.New("native agent must be nonempty and at most 512 MiB")
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return "", errors.New("native agent checksum does not match the release manifest")
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		return "", fmt.Errorf("make verified agent executable: %w", err)
	}
	path = filepath.Join(private, asset.Asset)
	if err := os.Rename(temporary, path); err != nil {
		return "", fmt.Errorf("publish verified agent executable: %w", err)
	}
	return path, nil
}
