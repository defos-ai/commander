package missions

import (
	"errors"
	"fmt"
	"strings"
)

type ContinueWhen string

const (
	WhenEach ContinueWhen = "when_each"
	WhenAll  ContinueWhen = "when_all"
)

type Decision string

const Model Decision = "model"

type Automation struct {
	Run      []string     `json:"run,omitempty"`
	Continue ContinueWhen `json:"continue,omitempty"`
	Decide   Decision     `json:"decide,omitempty"`
}

func (a Automation) Validate() error {
	if a.Continue != "" && a.Continue != WhenEach && a.Continue != WhenAll {
		return fmt.Errorf("continue must be %q or %q", WhenEach, WhenAll)
	}
	if a.Decide != "" && a.Decide != Model {
		return fmt.Errorf("unsupported decision %q", a.Decide)
	}
	if len(a.Run) == 0 && a.Decide == "" || len(a.Run) > 0 && a.Decide != "" {
		return errors.New("specify either run actions or decide model")
	}
	if a.Decide == Model && a.Continue == WhenAll {
		return errors.New("continue applies to generated children")
	}
	for _, action := range a.Run {
		if strings.TrimSpace(action) == "" {
			return errors.New("run action must not be blank")
		}
	}
	return nil
}
