//go:build (!unix && !windows) || aix

package docker

import "errors"

func lockState(string) (func() error, error) {
	return nil, errors.New("Docker spawner state locking is unsupported on this operating system")
}
