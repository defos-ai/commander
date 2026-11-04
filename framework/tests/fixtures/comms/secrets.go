package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

func TransientSecretRequest() secrets.Request {
	now := time.Now().UTC()
	return secrets.Request{
		ID: id.New(), Key: secrets.Key{Name: "VERIFICATION_CODE"}, Type: secrets.SecretTransient,
		AgentID: "browser-agent-01234567", AgentType: "browser-agent",
		OperationID: id.New(), TaskID: id.New(), CommandID: id.New(),
		Status: secrets.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}
