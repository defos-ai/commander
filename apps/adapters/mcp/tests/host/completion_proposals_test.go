package mcp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	fixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCompletionProposalAssignsModelOriginWithoutCompleting(t *testing.T) {
	const operationID = "00000000-0000-4000-8000-000000000001"
	commands := fakes.NewCompletionProposalCommands()
	ctx, session := fixtures.Session(t, adapter.Config{Operations: commands})
	for _, extra := range []string{`"proposed_by":"operator"`, `"id":"00000000-0000-4000-8000-000000000002"`} {
		_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "propose_completion", Arguments: json.RawMessage(
			`{"operation_id":"` + operationID + `","output":{},` + extra + `}`),
		})

		var invalid *jsonrpc.Error
		if !errors.As(err, &invalid) || invalid.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("chosen proposal provenance was accepted: %v", err)
		}
		select {
		case <-commands.Submitted():
			t.Fatal("invalid proposal reached controller")
		default:
		}
	}
	const output = `{"identifier":9007199254740993,"path":"\u0061\\path","summary":"Ready for operator review"}`

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "propose_completion", Arguments: json.RawMessage(
		`{"operation_id":"` + operationID + `","output":` + output + `}`),
	})

	text := completionToolText(t, result, err)
	if result.IsError {
		t.Fatalf("proposal was rejected: %s", text)
	}
	select {
	case command := <-commands.Submitted():
		if command.OperationID != operationID || command.ProposedBy != operations.CompletionModel || string(command.Output) != output {
			t.Fatal("MCP proposal changed output or provenance")
		}
	default:
		t.Fatal("proposal did not reach controller")
	}
	var saved operations.Operation
	if json.Unmarshal([]byte(text), &saved) != nil || saved.Status != operations.Planning || saved.ReportID != "" || saved.CompletedBy != "" || len(saved.Output) != 0 ||
		saved.CompletionProposal == nil || saved.CompletionProposal.ProposedBy != operations.CompletionModel ||
		!strings.Contains(text, "9007199254740993") || !strings.Contains(text, `\u0061\\path`) {
		t.Fatal("MCP proposal omitted saved evidence or implied accepted completion")
	}
}
