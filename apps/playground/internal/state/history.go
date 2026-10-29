package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
)

func (p *Profile) LoadHistory(scope string) (json.RawMessage, error) {
	if scope == "" {
		return nil, errors.New("conversation scope is required")
	}
	root, err := p.historyRoot(false)
	if err != nil || root == nil {
		return nil, err
	}
	defer root.Close()
	data, err := readFile(root, historyName(scope))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("saved conversation is invalid")
	}
	return json.RawMessage(data), nil
}

func (p *Profile) SaveHistory(scope string, history json.RawMessage) error {
	if scope == "" || !json.Valid(history) {
		return errors.New("conversation scope and valid JSON are required")
	}
	if _, err := p.LoadHistory(scope); err != nil {
		return err
	}
	root, err := p.historyRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeFile(root, historyName(scope), history)
}

func (p *Profile) historyRoot(create bool) (*os.Root, error) {
	if err := p.checkMarker(); err != nil {
		return nil, err
	}
	if err := regularDirectory(p.root, "histories", create); err != nil {
		return nil, err
	}
	root, err := p.root.OpenRoot("histories")
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil, nil
	}
	return root, err
}

func historyName(scope string) string {
	digest := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(digest[:]) + ".json"
}
