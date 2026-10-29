package runtimes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

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
func PinPackage(ctx context.Context, requestID, selector string, resolver *packages.Resolver) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := packages.RequireTrustedSource(ctx, selector); err != nil {
		return "", err
	}
	requested, err := agentpackages.ParseSelector(selector)
	if err != nil {
		return "", err
	}
	execution, _ := action.ExecutionFromContext(ctx)
	if !id.Valid(execution.CommandID) || requestID != "command:"+execution.CommandID {
		return "", errors.New("package checkpoint requires its execution command identity")
	}
	var reference string
	err = action.RunWithProgress(ctx, "Package version selection", func() error {
		var selectionErr error
		reference, selectionErr = pinPackage(ctx, requestID, requested, resolver)
		return selectionErr
	})
	return reference, err
}

func pinPackage(ctx context.Context, requestID string, requested agentpackages.Reference, resolver *packages.Resolver) (string, error) {
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
