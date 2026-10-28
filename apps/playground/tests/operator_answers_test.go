package playground_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestOperatorAnswerCommandUsesAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	op := fixtures.PendingQuestionOperation()
	u, server := fixtures.QuestionUI(t, ctx, op, fixtures.QuestionID)
	defer u.Close()
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("operator question", func(view string) bool { return strings.Contains(view, "Accept this release?") })

	u.Key(tea.KeyMsg{Type: tea.KeyTab})
	u.Text("/answer " + fixtures.QuestionID + " 2")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("command answer", func(view string) bool { return strings.Contains(view, "Operator answer: Decline") })
	u.Close()

	requests := server.APIRequests()
	if len(requests) != 1 || requests[0].Answer != "Decline" || requests[0].Username != "commander" || requests[0].Password != fixtures.QuestionAPIPassword || server.MCPAnswerCount() != 0 {
		t.Fatal("answer command did not resolve its option through the authenticated API exactly once")
	}
}

func TestOperatorAnswerConnectsAPIOutsideChat(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	op := fixtures.PendingQuestionOperation()
	u, server, apiURL := fixtures.QuestionUIWithOptions(t, ctx, op, fixtures.QuestionID, fixtures.QuestionUIOptions{NoAPILogin: true})
	defer u.Close()
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("operator question", func(view string) bool { return strings.Contains(view, "Accept this release?") })

	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("local API login", func(view string) bool { return strings.Contains(view, "Connect to API") })
	if len(server.APIRequests()) != 0 {
		t.Fatal("operator answer used an inferred API destination before local login")
	}
	u.Text(apiURL)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Text(fixtures.QuestionAPIPassword)
	if strings.Contains(u.Model.View(), fixtures.QuestionAPIPassword) {
		t.Fatal("operator answer API login exposed the password")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("authenticated answer", func(view string) bool { return strings.Contains(view, "Operator answer: Accept") })
	u.Close()

	requests := server.APIRequests()
	if len(requests) != 1 || requests[0].Answer != "Accept" || requests[0].Password != fixtures.QuestionAPIPassword || server.MCPAnswerCount() != 0 {
		t.Fatal("local login did not submit the retained operator answer directly to the API")
	}
}

func TestOperatorAnswerRetriesAuthenticationOnlyAfterLocalLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	op := fixtures.PendingQuestionOperation()
	u, server, _ := fixtures.QuestionUIWithOptions(t, ctx, op, fixtures.QuestionID, fixtures.QuestionUIOptions{APIPassword: "rejected-question-password"})
	defer u.Close()
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("operator question", func(view string) bool { return strings.Contains(view, "Accept this release?") })

	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("rejected API login", func(view string) bool { return strings.Contains(view, "Connect to API") })
	first := server.APIRequests()
	if len(first) != 1 || first[0].Password != "rejected-question-password" {
		t.Fatal("rejected login was retried before the operator entered new credentials")
	}
	u.Text(fixtures.QuestionAPIPassword)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("answer after explicit login", func(view string) bool { return strings.Contains(view, "Operator answer: Accept") })
	u.Close()

	requests := server.APIRequests()
	if len(requests) != 1 || requests[0].Password != fixtures.QuestionAPIPassword || requests[0].Answer != "Accept" || server.MCPAnswerCount() != 0 {
		t.Fatal("replacement login did not submit exactly one authenticated API answer")
	}
}

func TestOperatorAnswerUnconfirmedWritesAreNotRetried(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"server failure", http.StatusServiceUnavailable, `{"error":"operation_write_uncertain","detail":"private-api-response-detail"}`},
		{"invalid success", http.StatusOK, `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			op := fixtures.PendingQuestionOperation()
			u, server, _ := fixtures.QuestionUIWithOptions(t, ctx, op, fixtures.QuestionID,
				fixtures.QuestionUIOptions{API: fakes.QuestionAPIOptions{Status: test.status, ResponseBody: test.body}})
			defer u.Close()
			u.Text("/watch " + op.ID)
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("operator question", func(view string) bool { return strings.Contains(view, "Accept this release?") })

			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("unconfirmed API answer", func(view string) bool { return strings.Contains(view, "API write could not be confirmed") })
			u.Close()

			requests := server.APIRequests()
			view := u.Model.View()
			if len(requests) != 1 || server.MCPAnswerCount() != 0 || strings.Contains(view, "Operator answer: Accept") || strings.Contains(view, "private-api-response-detail") {
				t.Fatal("an unconfirmed answer was retried, treated as saved, exposed response details or used MCP")
			}
		})
	}
}

func TestOperatorAnswerExpiresDuringAPILogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	op := fixtures.PendingQuestionOperation()
	deadline := time.Now().Add(2 * time.Second)
	op.Questions[0].ExpiresAt = deadline
	u, server, apiURL := fixtures.QuestionUIWithOptions(t, ctx, op, fixtures.QuestionID, fixtures.QuestionUIOptions{NoAPILogin: true})
	defer u.Close()
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("operator question", func(view string) bool { return strings.Contains(view, "Accept this release?") })

	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("local API login", func(view string) bool { return strings.Contains(view, "Connect to API") })
	u.Wait("question deadline during login", func(string) bool { return time.Now().After(deadline) })
	u.Text(apiURL)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Text(fixtures.QuestionAPIPassword)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("expired answer discarded", func(view string) bool {
		return !strings.Contains(view, "Connect to API") && !strings.Contains(view, "Operator input")
	})
	u.Close()

	if len(server.APIRequests()) != 0 || server.MCPAnswerCount() != 0 || strings.Contains(u.Model.View(), "Operator answer: Accept") {
		t.Fatal("login completion submitted an answer to a question that had expired")
	}
}
