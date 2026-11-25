package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/testing/missiontest"
)

func (a *Client) MissionDriver() *missiontest.Driver {
	a.t.Helper()
	return missiontest.Connect(a.t, missiontest.Config{
		URL:         a.url,
		Credentials: host.BasicCredentials{Username: a.Username, Password: a.password},
		HTTPClient:  a.Client,
		Timeout:     90 * time.Second,
	})
}
