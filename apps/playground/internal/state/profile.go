package state

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
)

const maxFileSize = 32 << 20

type Settings struct {
	Provider string             `json:"provider,omitempty"`
	Model    string             `json:"model,omitempty"`
	BaseURL  string             `json:"base_url,omitempty"`
	Remote   ConnectionSettings `json:"remote"`
}

type ConnectionSettings struct {
	MCPURL      string `json:"mcp_url,omitempty"`
	APIURL      string `json:"api_url,omitempty"`
	APIUsername string `json:"api_username,omitempty"`
}

type Profile struct {
	Directory string
	root      *os.Root
	lock      *os.File
}

type marker struct {
	Application string `json:"application"`
	Version     int    `json:"version"`
}

func Open(directory string) (*Profile, error) {
	if directory == "" {
		return nil, errors.New("profile directory is required")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("profile path must be a directory, not a symbolic link")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	p := &Profile{Directory: directory, root: root}
	if err := p.initialize(info); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *Profile) initialize(expected os.FileInfo) error {
	directory, err := p.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	actual, err := directory.Stat()
	if err != nil || !os.SameFile(expected, actual) {
		return errors.New("profile directory changed while opening")
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	newProfile := len(entries) == 0
	if !newProfile {
		if err := p.checkMarker(); err != nil {
			return err
		}
	}
	p.lock, err = openRegular(p.root, ".lock", true)
	if err != nil {
		return err
	}
	if err := lockFile(p.lock); err != nil {
		return err
	}
	if err := p.lock.Chmod(0600); err != nil {
		return err
	}
	if newProfile {
		entries, err := fs.ReadDir(p.root.FS(), ".")
		if err != nil || len(entries) != 1 || entries[0].Name() != ".lock" {
			return errors.New("profile directory is no longer empty; refusing to overwrite it")
		}
		if err := writeJSON(p.root, "profile.json", marker{Application: "commander", Version: 1}); err != nil {
			return err
		}
	}
	if _, err := p.Load(); err != nil {
		return err
	}
	if _, err := p.loadKeys(); err != nil {
		return err
	}
	if err := regularDirectory(p.root, "histories", false); err != nil {
		return err
	}
	return directory.Chmod(0700)
}

func (p *Profile) checkMarker() error {
	if p == nil || p.root == nil {
		return os.ErrClosed
	}
	data, err := readFile(p.root, "profile.json")
	if err != nil {
		return fmt.Errorf("profile ownership marker is missing or unreadable; refusing to overwrite directory: %w", err)
	}
	var value marker
	if decodeJSON(data, &value) != nil || value.Application != "commander" || value.Version != 1 {
		return errors.New("profile ownership marker is invalid or incompatible; refusing to overwrite directory")
	}
	return nil
}

func (p *Profile) Load() (Settings, error) {
	var settings Settings
	if err := p.checkMarker(); err != nil {
		return settings, err
	}
	data, err := readFile(p.root, "settings.json")
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if decodeJSON(data, &settings) != nil || validateSettings(settings) != nil {
		return Settings{}, errors.New("saved Playground settings are invalid")
	}
	return settings, nil
}

func (p *Profile) Save(settings Settings) error {
	if _, err := p.Load(); err != nil {
		return err
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	return writeJSON(p.root, "settings.json", settings)
}

func validateSettings(settings Settings) error {
	for _, value := range []string{settings.BaseURL, settings.Remote.MCPURL, settings.Remote.APIURL} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
			return errors.New("saved endpoint URLs must use HTTP or HTTPS without credentials, query or fragment")
		}
	}
	return nil
}

func (p *Profile) Close() error {
	if p == nil || p.root == nil {
		return nil
	}
	var err error
	if p.lock != nil {
		err = p.lock.Close()
		p.lock = nil
	}
	err = errors.Join(err, p.root.Close())
	p.root = nil
	return err
}

func readFile(root *os.Root, name string) ([]byte, error) {
	file, err := openRegular(root, name, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if len(data) > maxFileSize {
		clear(data)
		return nil, errors.New("saved Playground state exceeds 32 MiB")
	}
	return data, err
}

func decodeJSON(data []byte, value any) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("saved state must contain a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("saved state must contain one JSON value")
	}
	return nil
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(data)
	return writeFile(root, name, data)
}

func writeFile(root *os.Root, name string, data []byte) error {
	if len(data) > maxFileSize {
		return errors.New("saved Playground state exceeds 32 MiB")
	}
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return errors.New("saved state must be a regular file, not a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := "." + name + "-" + rand.Text() + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncRoot(root)
}

func regularDirectory(root *os.Root, name string, create bool) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if create {
			return root.Mkdir(name, 0700)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("saved state directory must not be a symbolic link")
	}
	return nil
}
