package local

import (
	"fmt"
	"os"

	"github.com/defos-ai/commander/framework/host"
)

func apiCredentials() (host.Credentials, error) {
	value := host.Credentials{Username: os.Getenv("API_USERNAME"), Password: os.Getenv("API_PASSWORD")}
	if value.Username == "" {
		value.Username = "commander"
	}
	if value.Password == "" {
		var err error
		value.Password, err = host.GeneratePassword()
		if err != nil {
			return host.Credentials{}, fmt.Errorf("generate API password: %w", err)
		}
	}
	return value, nil
}
