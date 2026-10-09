package api

import (
	"errors"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

type DeploymentConfig struct {
	Runtime     string
	Application string
}

func (c DeploymentConfig) Validate() error {
	switch c.Runtime {
	case "", "native", "docker", "docker-compose":
	default:
		return errors.New("runtime must be native, docker or docker-compose (API_RUNTIME)")
	}
	switch c.Application {
	case "", "commander-api", "commander":
	default:
		return errors.New("application must be commander-api or commander")
	}
	return nil
}

func (c DeploymentConfig) response() models.AboutResponse {
	if c.Runtime == "" {
		c.Runtime = "native"
	}
	if c.Application == "" {
		c.Application = "commander-api"
	}
	return models.AboutResponse{Runtime: c.Runtime, Application: c.Application}
}
