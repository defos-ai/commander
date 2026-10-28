package fakes

import (
	"github.com/defos-ai/commander/framework/agent/action"
)

type BuildSecrets string

func (s BuildSecrets) Secret(name string) ([]byte, error) {
	if name != "OPENAI_API_KEY" {
		return nil, action.ErrSecretUnavailable
	}
	return []byte(s), nil
}
