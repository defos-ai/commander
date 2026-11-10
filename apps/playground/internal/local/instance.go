package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

var ErrNotRunning = errors.New("local Commander is not running")

type Instance struct {
	Version   int       `json:"version"`
	ID        string    `json:"instance_id"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	MCPURL    string    `json:"mcp_url"`
	APIURL    string    `json:"api_url"`
	AdminURL  string    `json:"admin_url,omitempty"`
}

func Discover(ctx context.Context, directory string) (Instance, error) {
	root, err := openExistingStateDirectory(directory)
	if errors.Is(err, os.ErrNotExist) {
		return Instance{}, ErrNotRunning
	}
	if err != nil {
		return Instance{}, err
	}
	defer root.Close()
	instance, err := readInstance(root)
	if errors.Is(err, os.ErrNotExist) {
		return Instance{}, ErrNotRunning
	}
	if err != nil {
		return Instance{}, err
	}
	for _, address := range []string{instance.APIURL, instance.MCPURL, instance.AdminURL} {
		if address != "" && !loopbackURL(address) {
			return Instance{}, errors.New("local instance discovery requires literal loopback addresses")
		}
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, address := range []string{instance.APIURL, instance.MCPURL} {
		if err := probeInstance(probe, client, address, instance.ID); err != nil {
			if ctx.Err() != nil {
				return Instance{}, ctx.Err()
			}
			return Instance{}, ErrNotRunning
		}
	}
	return instance, nil
}

func probeInstance(ctx context.Context, client *http.Client, address, instanceID string) error {
	endpoint, err := url.Parse(address)
	if err != nil {
		return err
	}
	endpoint.Path = "/aboutz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrNotRunning
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(data) > 4096 {
		return ErrNotRunning
	}
	var metadata struct {
		Application string `json:"application"`
		InstanceID  string `json:"instance_id"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Application != "commander" || metadata.InstanceID != instanceID {
		return ErrNotRunning
	}
	return nil
}

func loopbackURL(raw string) bool {
	endpoint, err := url.Parse(raw)
	return err == nil && net.ParseIP(endpoint.Hostname()).IsLoopback()
}

func readInstance(root *os.Root) (Instance, error) {
	var instance Instance
	data, err := readStateFile(root, "instance.json", 16<<10)
	if err != nil {
		return instance, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&instance) != nil || decoder.Decode(new(any)) != io.EOF {
		return Instance{}, errors.New("invalid local instance record")
	}
	if err := instance.validate(); err != nil {
		return Instance{}, err
	}
	return instance, nil
}

func (i Instance) validate() error {
	if i.Version != 1 || !id.Valid(i.ID) || i.PID < 1 || i.StartedAt.IsZero() {
		return errors.New("invalid local instance identity")
	}
	for _, entry := range []struct {
		address, path string
		optional      bool
	}{{i.APIURL, "", false}, {i.MCPURL, "/mcp", false}, {i.AdminURL, "", true}} {
		if entry.optional && entry.address == "" {
			continue
		}
		endpoint, err := url.Parse(entry.address)
		if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" ||
			(endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Path != entry.path || net.ParseIP(endpoint.Hostname()) == nil {
			return errors.New("invalid local instance endpoint")
		}
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("invalid local instance endpoint port")
		}
	}
	return nil
}

func (s *Session) saveInstance() error {
	if !s.persistent {
		return nil
	}
	instance := Instance{Version: 1, ID: s.InstanceID, PID: os.Getpid(), StartedAt: s.startedAt, MCPURL: s.MCPURL, APIURL: s.APIURL, AdminURL: s.AdminURL}
	if err := instance.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(instance)
	if err != nil {
		return err
	}
	name := "instance-" + s.InstanceID + ".tmp"
	file, err := s.stateRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer s.stateRoot.Remove(name)
	_, err = file.Write(data)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := s.stateRoot.Rename(name, "instance.json"); err != nil {
		return err
	}
	s.instanceSaved = true
	return nil
}

func (s *Session) removeInstance(expected string) error {
	if s.stateRoot == nil || s.stateLock == nil {
		return nil
	}
	info, err := s.stateRoot.Lstat("instance.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("local instance record must be a regular file")
	}
	if expected != "" {
		instance, err := readInstance(s.stateRoot)
		if err != nil {
			return err
		}
		if instance.ID != expected {
			return errors.New("local instance record changed; preserving it")
		}
	}
	if err := s.stateRoot.Remove("instance.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove local instance record: %w", err)
	}
	return nil
}
