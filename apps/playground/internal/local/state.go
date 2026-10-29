package local

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/host"
)

const bootstrapFormat = "commander-local-v1"

type bootstrap struct {
	Format  string                 `json:"format"`
	API     host.BasicCredentials  `json:"api"`
	MCP     host.BearerCredentials `json:"mcp"`
	KeyHash string                 `json:"encryption_key_sha256"`
}

func SavedCredentials(directory string) (host.BasicCredentials, host.BearerCredentials, error) {
	root, err := openExistingStateDirectory(directory)
	if err != nil {
		return host.BasicCredentials{}, host.BearerCredentials{}, err
	}
	defer root.Close()
	saved, _, err := loadState(root)
	if err != nil {
		return host.BasicCredentials{}, host.BearerCredentials{}, err
	}
	return saved.API, saved.MCP, nil
}

func (s *Session) prepareState(directory string) (*crypto.SecretCipher, error) {
	configuredKey := os.Getenv("SECRETS_ENCRYPTION_KEY")
	if configuredKey != "" {
		if _, err := crypto.NewSecretCipher(configuredKey); err != nil {
			return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY: %w", err)
		}
	}
	if directory == "" {
		var err error
		if s.APICredentials, err = apiCredentials(); err != nil {
			return nil, err
		}
		if s.MCPCredentials, err = mcpCredentials(); err != nil {
			return nil, err
		}
		cipher, err := secretCipher()
		if err != nil {
			return nil, err
		}
		s.Directory, err = os.MkdirTemp("", "commander-playground-*")
		return cipher, err
	}
	s.persistent = true
	var err error
	s.Directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	root, empty, err := openStateDirectory(s.Directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var saved bootstrap
	var key string
	if empty {
		saved, key, err = initializeState(root, configuredKey)
	} else {
		saved, key, err = loadState(root)
	}
	if err != nil {
		return nil, err
	}
	if err := checkOverrides(saved, key, configuredKey); err != nil {
		return nil, err
	}
	s.APICredentials, s.MCPCredentials = saved.API, saved.MCP
	return crypto.NewSecretCipher(key)
}

func openStateDirectory(directory string) (*os.Root, bool, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, false, fmt.Errorf("create local state directory: %w", err)
	}
	root, err := openExistingStateDirectory(directory)
	if err != nil {
		return nil, false, err
	}
	directoryFile, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, false, err
	}
	entries, err := directoryFile.ReadDir(1)
	directoryFile.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		root.Close()
		return nil, false, err
	}
	return root, len(entries) == 0, nil
}

func openExistingStateDirectory(directory string) (*os.Root, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local state directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("local state directory must have private permissions (0700)")
	}
	return os.OpenRoot(directory)
}

func initializeState(root *os.Root, key string) (bootstrap, string, error) {
	var saved bootstrap
	var err error
	if saved.API, err = apiCredentials(); err != nil {
		return saved, "", err
	}
	if err := saved.API.Validate(); err != nil {
		return saved, "", fmt.Errorf("API credentials: %w", err)
	}
	if saved.MCP, err = mcpCredentials(); err != nil {
		return saved, "", err
	}
	if key == "" {
		if key, err = host.GeneratePassword(); err != nil {
			return saved, "", fmt.Errorf("generate secrets encryption key: %w", err)
		}
	}
	key = strings.ToLower(key)
	saved.Format, saved.KeyHash = bootstrapFormat, keyHash(key)
	encoded, err := json.Marshal(saved)
	if err != nil {
		return saved, "", errors.New("encode local credentials")
	}
	if err := writeStateFile(root, "secrets.key", []byte(key)); err != nil {
		return saved, "", err
	}
	if err := writeStateFile(root, "credentials.json", encoded); err != nil {
		return saved, "", err
	}
	// Bootstrap must reach disk before hosts can save encrypted database records.
	if runtime.GOOS != "windows" {
		directory, err := root.Open(".")
		if err != nil {
			return saved, "", err
		}
		err = errors.Join(directory.Sync(), directory.Close())
		if err != nil {
			return saved, "", fmt.Errorf("sync local state directory: %w", err)
		}
	}
	return saved, key, nil
}

func loadState(root *os.Root) (bootstrap, string, error) {
	var saved bootstrap
	encoded, err := readStateFile(root, "credentials.json", 64<<10)
	if err != nil {
		return saved, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF || saved.Format != bootstrapFormat || saved.API.Validate() != nil || saved.MCP.Validate() != nil {
		return saved, "", errors.New("invalid local credentials; restore the saved local bootstrap")
	}
	encodedKey, err := readStateFile(root, "secrets.key", 64)
	if err != nil {
		return saved, "", err
	}
	key := string(encodedKey)
	if _, err := crypto.NewSecretCipher(key); err != nil || saved.KeyHash != keyHash(key) {
		return saved, "", errors.New("invalid local encryption key; restore the original saved key")
	}
	return saved, key, nil
}

func readStateFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("read local %s: %w; restore the saved local bootstrap", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > limit || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, fmt.Errorf("local %s must be a bounded private regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("local %s changed while opening", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("cannot read local %s", name)
	}
	return data, nil
}

func writeStateFile(root *os.Root, name string, value []byte) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create local %s: %w", name, err)
	}
	_, err = file.Write(value)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return fmt.Errorf("save local %s: %w", name, err)
	}
	return nil
}

func checkOverrides(saved bootstrap, key, configuredKey string) error {
	for _, check := range []struct{ name, saved string }{
		{"API_USERNAME", saved.API.Username}, {"API_PASSWORD", saved.API.Password}, {"MCP_TOKEN", saved.MCP.Token},
	} {
		if value := os.Getenv(check.name); value != "" && value != check.saved {
			return fmt.Errorf("%s conflicts with saved local credentials", check.name)
		}
	}
	if configuredKey != "" && !strings.EqualFold(configuredKey, key) {
		return errors.New("SECRETS_ENCRYPTION_KEY conflicts with the saved local encryption key")
	}
	return nil
}

func keyHash(key string) string {
	decoded, _ := hex.DecodeString(key)
	defer clear(decoded)
	hash := sha256.Sum256(decoded)
	return hex.EncodeToString(hash[:])
}
