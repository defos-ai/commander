package runtimes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent/action"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

type packageSelection struct {
	RequestID string `json:"request_id"`
	Selector  string `json:"selector"`
	Package   string `json:"package"`
}

// PinPackage saves the selected version before downloads or image pulls. The
// separate launch intent still fences process/container creation: selecting a
// package alone never means that an agent has started.
func PinPackage(ctx context.Context, directory, requestID, selector string, resolver *packages.Resolver) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(requestID) == "" || len(requestID) > 1024 {
		return "", errors.New("launch request identity is required and must be at most 1024 bytes")
	}
	requested, err := agentpackages.ParseSelector(selector)
	if err != nil {
		return "", err
	}
	if UsesExecutionCheckpoints(ctx) {
		return pinExecutionPackage(ctx, requestID, requested, resolver)
	}
	digest := sha256.Sum256([]byte(requestID))
	path := filepath.Join(directory, "records", hex.EncodeToString(digest[:])+".package.json")
	if saved, exists, err := readSelection(path, requestID, requested); err != nil || exists {
		return saved.Package, err
	}
	resolved, err := resolver.Pin(ctx, requested.String())
	if err != nil {
		return "", err
	}
	saved := packageSelection{RequestID: requestID, Selector: requested.String(), Package: resolved.String()}
	if err := saved.validate(requestID, requested); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return "", err
	}
	if len(data) > 16*1024 {
		return "", errors.New("package selection exceeds the launch record size limit")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		winner, exists, err := readSelection(path, requestID, requested)
		if err == nil && !exists {
			return "", errors.New("package selection disappeared; refusing to resolve another version")
		}
		return winner.Package, err
	}
	if err != nil {
		return "", fmt.Errorf("reserve package selection: %w", err)
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		// Retain incomplete selections too; uncertain state cannot authorize a
		// different version on retry.
		return "", fmt.Errorf("persist package selection: %w", err)
	}
	if err := syncSelectionDirectory(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("persist package selection directory: %w", err)
	}
	return saved.Package, nil
}

func pinExecutionPackage(ctx context.Context, requestID string, requested agentpackages.Reference, resolver *packages.Resolver) (string, error) {
	execution, _ := action.ExecutionFromContext(ctx)
	if !id.Valid(execution.CommandID) || requestID != "command:"+execution.CommandID {
		return "", errors.New("package checkpoint requires its execution command identity")
	}
	data, exists, err := action.LoadCheckpoint(ctx, "package-selection")
	if err != nil {
		return "", err
	}
	if exists {
		var saved packageSelection
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
			return "", errors.New("invalid package checkpoint; refusing to resolve another version")
		}
		if err := saved.validate(requestID, requested); err != nil {
			return "", err
		}
		// A previous save may have returned an uncertain write. Confirm the
		// identical value's durability before using it for further preparation.
		if err := action.SaveCheckpoint(ctx, "package-selection", data); err != nil {
			return "", err
		}
		return saved.Package, nil
	}
	resolved, err := resolver.Pin(ctx, requested.String())
	if err != nil {
		return "", err
	}
	saved := packageSelection{RequestID: requestID, Selector: requested.String(), Package: resolved.String()}
	if err := saved.validate(requestID, requested); err != nil {
		return "", err
	}
	data, err = json.Marshal(saved)
	if err != nil {
		return "", err
	}
	if err := action.SaveCheckpoint(ctx, "package-selection", data); err != nil {
		return "", err
	}
	return saved.Package, nil
}

func readSelection(path, requestID string, requested agentpackages.Reference) (packageSelection, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return packageSelection{}, false, nil
	}
	if err != nil {
		return packageSelection{}, true, fmt.Errorf("read package selection: %w", err)
	}
	defer file.Close()
	invalid := errors.New("package selection is invalid or incomplete; refusing to resolve another version")
	info, err := file.Stat()
	if err != nil || info.Size() > 16*1024 || !info.Mode().IsRegular() {
		return packageSelection{}, true, invalid
	}
	var saved packageSelection
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
		return packageSelection{}, true, invalid
	}
	if err := saved.validate(requestID, requested); err != nil {
		return packageSelection{}, true, err
	}
	// A concurrent creator can publish the complete bytes before flushing them.
	// Flush the winning selection before allowing either caller to download.
	if err := errors.Join(file.Sync(), syncSelectionDirectory(filepath.Dir(path))); err != nil {
		return packageSelection{}, true, fmt.Errorf("persist selected package: %w", err)
	}
	return saved, true, nil
}

func (s packageSelection) validate(requestID string, requested agentpackages.Reference) error {
	selector, selectorErr := agentpackages.ParseSelector(s.Selector)
	pinned, pinErr := agentpackages.ParseReference(s.Package)
	if s.RequestID != requestID || selectorErr != nil || pinErr != nil || selector.String() != s.Selector || pinned.String() != s.Package ||
		selector.Owner != pinned.Owner || selector.Repository != pinned.Repository || selector.ModulePath != pinned.ModulePath ||
		(selector.Version != "stable" && selector.Version != pinned.Version) {
		return errors.New("package selection is invalid or incomplete; refusing to resolve another version")
	}
	if s.Selector != requested.String() {
		return errors.New("launch request already belongs to a different package")
	}
	return nil
}
