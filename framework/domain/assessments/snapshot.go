// Package assessments defines the immutable application-level scope of an operation.
// It validates explicitly annotated destinations, not arbitrary command traffic.
package assessments

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

type Network struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	CIDRs []string `json:"cidrs"`
}

type Domain struct {
	Hostname          string `json:"hostname"`
	IncludeSubdomains bool   `json:"include_subdomains"`
}

type Scope struct {
	Networks []Network `json:"networks"`
	Domains  []Domain  `json:"domains"`
}

type Snapshot struct {
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Revision   int    `json:"revision"`
	NetworkID  string `json:"network_id,omitempty"`
	Scope      Scope  `json:"scope"`
}

func (s *Snapshot) Clone() *Snapshot {
	if s == nil {
		return nil
	}
	result := *s
	result.Scope.Domains = slices.Clone(s.Scope.Domains)
	result.Scope.Networks = slices.Clone(s.Scope.Networks)
	for i := range result.Scope.Networks {
		result.Scope.Networks[i].CIDRs = slices.Clone(s.Scope.Networks[i].CIDRs)
	}
	return &result
}

func (s *Snapshot) Validate() error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(s.TargetID) == "" || s.Revision < 1 || len(s.Scope.Networks)+len(s.Scope.Domains) == 0 {
		return errors.New("assessment requires a Target, revision and nonempty scope")
	}
	ids := map[string]bool{}
	for _, network := range s.Scope.Networks {
		if network.ID == "" || ids[network.ID] || len(network.CIDRs) == 0 {
			return errors.New("assessment Network requires a distinct identity and CIDRs")
		}
		ids[network.ID] = true
		for _, cidr := range network.CIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				return fmt.Errorf("invalid assessment CIDR: %w", err)
			}
		}
	}
	if s.NetworkID != "" && !ids[s.NetworkID] {
		return errors.New("selected Network is not in assessment scope")
	}
	for _, domain := range s.Scope.Domains {
		if !validHostname(domain.Hostname) {
			return errors.New("assessment domain must be an exact ASCII hostname; use include_subdomains explicitly")
		}
	}
	return nil
}

func validHostname(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.ContainsAny(value, "/:*@ ") {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func destination(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" {
			return ""
		}
		return parsed.Hostname()
	}
	return value
}

func (s *Snapshot) CheckDestination(value string) error {
	if s == nil {
		return nil
	}
	value = destination(value)
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		if address, addressErr := netip.ParseAddr(value); addressErr == nil {
			prefix, err = netip.PrefixFrom(address, address.BitLen()), nil
		}
	}
	if err == nil {
		matches := map[string]bool{}
		for _, network := range s.Scope.Networks {
			for _, text := range network.CIDRs {
				allowed, parseErr := netip.ParsePrefix(text)
				if parseErr == nil && allowed.Addr().BitLen() == prefix.Addr().BitLen() && allowed.Bits() <= prefix.Bits() && allowed.Masked().Contains(prefix.Masked().Addr()) {
					matches[network.ID] = true
				}
			}
		}
		if s.NetworkID == "" && len(matches) > 1 {
			return errors.New("assessment destination matches multiple Networks; select a Network")
		}
		if s.NetworkID != "" && matches[s.NetworkID] || s.NetworkID == "" && len(matches) == 1 {
			return nil
		}
	} else {
		name := strings.ToLower(strings.TrimSuffix(value, "."))
		if validHostname(name) {
			for _, entry := range s.Scope.Domains {
				allowed := strings.ToLower(strings.TrimSuffix(entry.Hostname, "."))
				if name == allowed || entry.IncludeSubdomains && strings.HasSuffix(name, "."+allowed) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("destination %q is outside operation assessment scope", value)
}

// ValidateInput only reads x-defos-destination annotations supplied in public schemas.
func (s *Snapshot) ValidateInput(schema, input json.RawMessage) error {
	if s == nil {
		return nil
	}
	if err := s.Validate(); err != nil {
		return err
	}
	var root map[string]any
	var value any
	if err := json.Unmarshal(schema, &root); err != nil {
		return err
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return err
	}
	var walk func(map[string]any, any, int) error
	walk = func(node map[string]any, current any, depth int) error {
		if depth > 64 {
			return errors.New("assessment schema nesting is too deep")
		}
		if ref, ok := node["$ref"].(string); ok {
			if !strings.HasPrefix(ref, "#/") {
				return errors.New("assessment schemas must use local references")
			}
			var resolved any = root
			for _, token := range strings.Split(ref[2:], "/") {
				object, ok := resolved.(map[string]any)
				if !ok {
					return errors.New("invalid assessment schema reference")
				}
				resolved = object[strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")]
			}
			if object, ok := resolved.(map[string]any); ok {
				if err := walk(object, current, depth+1); err != nil {
					return err
				}
			}
		}
		if annotated, _ := node["x-defos-destination"].(bool); annotated {
			values, array := current.([]any)
			if !array {
				values = []any{current}
			}
			for _, item := range values {
				text, ok := item.(string)
				if !ok || text == "" {
					return errors.New("declared destination must be a nonempty string")
				}
				if err := s.CheckDestination(text); err != nil {
					return err
				}
			}
		}
		if object, ok := current.(map[string]any); ok {
			properties, _ := node["properties"].(map[string]any)
			for key, child := range properties {
				childSchema, schemaOK := child.(map[string]any)
				item, exists := object[key]
				if schemaOK && exists {
					if err := walk(childSchema, item, depth+1); err != nil {
						return err
					}
				}
			}
		}
		if array, ok := current.([]any); ok {
			if items, ok := node["items"].(map[string]any); ok {
				for _, item := range array {
					if err := walk(items, item, depth+1); err != nil {
						return err
					}
				}
			}
		}
		for _, composition := range []string{"allOf", "anyOf", "oneOf"} {
			branches, _ := node[composition].([]any)
			for _, branch := range branches {
				if object, ok := branch.(map[string]any); ok {
					if err := walk(object, current, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return walk(root, value, 0)
}
