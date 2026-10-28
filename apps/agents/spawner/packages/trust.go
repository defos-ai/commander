package packages

import (
	"errors"
	"strings"

	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

var ErrSourceApprovalRequired = errors.New("agent source requires operator approval; only github.com/defos-ai sources are currently supported")

func RequireTrustedSource(selector string) error {
	reference, err := agentpackages.ParseSelector(selector)
	if err != nil {
		return err
	}
	if !strings.EqualFold(reference.Owner, "defos-ai") {
		return ErrSourceApprovalRequired
	}
	return nil
}
