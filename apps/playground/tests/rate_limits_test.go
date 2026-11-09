package tests

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestOpenAIRateLimitRetriesHonorServerWaitAndPreserveCompletedCalls(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		reply fakes.RateLimitReply
		wait  time.Duration
	}{
		{
			name: "HTTP headers", wait: 40 * time.Millisecond,
			reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests,
				Headers: http.Header{"Retry-After-Ms": {"40"}},
				Body:    fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after-ms": "10"})},
		},
		{
			name: "initial streaming error from reported failure", wait: time.Second,
			reply: fakes.RateLimitReply{Status: http.StatusOK,
				Body: "data: " + `{"type":"response.created","response":{"output":[]}}` + "\n\n" +
					fixtures.RateLimitStream("error", "rate_limit_exceeded", map[string]string{"retry-after": "1", "retry-after-ms": "789"})},
		},
		{
			name: "failed initial response", wait: 40 * time.Millisecond,
			reply: fakes.RateLimitReply{Status: http.StatusOK,
				Body: fixtures.RateLimitStream("response.failed", "rate_limit_exceeded", map[string]string{"retry-after-ms": "40"})},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := fakes.NewRateLimitProvider(scenario.reply, fakes.RateLimitReply{Status: http.StatusOK, Body: fixtures.CompletedRateLimitStream})
			client := fixtures.NewRateLimitClient(t, provider, false)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()

			result, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)

			if err != nil {
				t.Fatal(err)
			}
			if len(result.Calls) != 1 || result.Calls[0].ID != "fixture-call" || result.Calls[0].Name != "list_agents" || string(result.Calls[0].Arguments) != "{}" || !bytes.Contains(result.Native, []byte("opaque-fixture")) {
				t.Fatal("retry did not preserve the authoritative completed response")
			}
			times, bodies := provider.Snapshot()
			if len(times) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
				t.Fatal("expected exactly one retry of the identical model request")
			}
			if times[1].Sub(times[0]) < scenario.wait {
				t.Fatal("retry happened before the longest server-directed delay")
			}
		})
	}
}

func TestOpenAIRateLimitRetryHonorsHTTPDate(t *testing.T) {
	until := time.Now().UTC().Add(2 * time.Second).Truncate(time.Second)
	provider := fakes.NewRateLimitProvider(fakes.RateLimitReply{
		Status: http.StatusTooManyRequests, Headers: http.Header{"Retry-After": {until.Format(http.TimeFormat)}},
		Body: fixtures.RateLimitBody("rate_limit_exceeded", nil),
	}, fakes.RateLimitReply{Status: http.StatusOK, Body: fixtures.CompletedRateLimitStream})
	client := fixtures.NewRateLimitClient(t, provider, false)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	_, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)

	if err != nil {
		t.Fatal(err)
	}
	times, _ := provider.Snapshot()
	if len(times) != 2 || times[1].Before(until) {
		t.Fatal("retry happened before the HTTP Retry-After date")
	}
}

func TestOpenAIUncertainStreamsAndNonRateLimitFailuresAreNotRetried(t *testing.T) {
	rateFailure := fixtures.RateLimitStream("error", "rate_limit_exceeded", map[string]string{"retry-after-ms": "0"})
	for _, scenario := range []struct {
		name  string
		reply fakes.RateLimitReply
	}{
		{name: "quota", reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests, Headers: http.Header{"Retry-After": {"0"}}, Body: fixtures.RateLimitBody("insufficient_quota", nil)}},
		{name: "authentication", reply: fakes.RateLimitReply{Status: http.StatusUnauthorized, Headers: http.Header{"Retry-After": {"0"}}, Body: fixtures.RateLimitBody("rate_limit_exceeded", nil)}},
		{name: "server failure", reply: fakes.RateLimitReply{Status: http.StatusInternalServerError, Headers: http.Header{"Retry-After": {"0"}}, Body: fixtures.RateLimitBody("rate_limit_exceeded", nil)}},
		{name: "missing retry advice", reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests, Body: fixtures.RateLimitBody("rate_limit_exceeded", nil)}},
		{name: "invalid retry advice", reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests, Body: fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after": "1m", "retry-after-ms": "-1"})}},
		{name: "NaN retry advice", reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests, Body: fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after-ms": "NaN"})}},
		{name: "infinite retry advice", reply: fakes.RateLimitReply{Status: http.StatusTooManyRequests, Body: fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after-ms": "Inf"})}},
		{name: "text output", reply: fakes.RateLimitReply{Status: http.StatusOK, Body: "data: " + `{"type":"response.output_text.delta","delta":"Partial"}` + "\n\n" + rateFailure}},
		{name: "reasoning output", reply: fakes.RateLimitReply{Status: http.StatusOK, Body: "data: " + `{"type":"response.output_item.added","item":{"type":"reasoning"}}` + "\n\n" + rateFailure}},
		{name: "tool output", reply: fakes.RateLimitReply{Status: http.StatusOK, Body: "data: " + `{"type":"response.function_call_arguments.delta","delta":"{}"}` + "\n\n" + rateFailure}},
		{name: "unknown event", reply: fakes.RateLimitReply{Status: http.StatusOK, Body: "data: " + `{"type":"provider.new_event"}` + "\n\n" + rateFailure}},
		{name: "ended incomplete stream", reply: fakes.RateLimitReply{Status: http.StatusOK, Body: "data: " + `{"type":"response.created","response":{"output":[]}}` + "\n\n"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := fakes.NewRateLimitProvider(scenario.reply)
			client := fixtures.NewRateLimitClient(t, provider, false)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			result, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)

			if err == nil {
				t.Fatal("expected the failed model request to be reported")
			}
			if strings.Contains(err.Error(), "provider-diagnostic-only") || strings.Contains(err.Error(), fixtures.RateLimitProviderKey) {
				t.Fatal("normal error output exposed provider diagnostics")
			}
			times, _ := provider.Snapshot()
			if len(times) != 1 || len(result.Calls) != 0 || len(result.Native) != 0 {
				t.Fatal("uncertain request was replayed or returned partial executable output")
			}
		})
	}
}

func TestOpenAIRateLimitRetryAttemptsAndWaitAreBounded(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{name: "two retries", headers: map[string]string{"retry-after-ms": "0"}, want: 3},
		{name: "server delay exceeds budget", headers: map[string]string{"retry-after-ms": "31000"}, want: 1},
		{name: "server delay exceeds duration range", headers: map[string]string{"retry-after": "99999999999999999999", "retry-after-ms": "0"}, want: 1},
		{name: "server delay exceeds numeric range", headers: map[string]string{"retry-after": strings.Repeat("9", 400), "retry-after-ms": "0"}, want: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := fakes.NewRateLimitProvider(fakes.RateLimitReply{Status: http.StatusTooManyRequests,
				Body: fixtures.RateLimitBody("rate_limit_exceeded", scenario.headers)})
			client := fixtures.NewRateLimitClient(t, provider, false)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)

			if err == nil || !strings.Contains(err.Error(), "model token-per-minute limit reached") || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("retry budget did not return the provider rate-limit error")
			}
			times, _ := provider.Snapshot()
			if len(times) != scenario.want {
				t.Fatalf("expected %d requests, got %d", scenario.want, len(times))
			}
		})
	}
}

func TestOpenAIRateLimitWaitIsCanceledWithoutAnotherRequest(t *testing.T) {
	provider := fakes.NewRateLimitProvider(fakes.RateLimitReply{Status: http.StatusTooManyRequests,
		Body: fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after": "30"})})
	client := fixtures.NewRateLimitClient(t, provider, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)
		finished <- err
	}()
	select {
	case <-provider.Responded():
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not return its rate-limit rejection")
	}

	cancel()

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation while waiting, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceling the rate-limit wait did not stop the model request")
	}
	times, _ := provider.Snapshot()
	if len(times) != 1 {
		t.Fatal("cancellation replayed the rejected request")
	}
}

func TestOpenAIRateLimitDebugDetailsStayRedacted(t *testing.T) {
	provider := fakes.NewRateLimitProvider(fakes.RateLimitReply{Status: http.StatusTooManyRequests,
		Body: fixtures.RateLimitBody("rate_limit_exceeded", map[string]string{"retry-after": "31", "authorization": "Bearer " + fixtures.RateLimitProviderKey})})
	client := fixtures.NewRateLimitClient(t, provider, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Complete(ctx, "Fixture instructions", []llm.Message{{Role: "user", Text: "Hello"}}, nil, nil)

	if err == nil || !strings.Contains(err.Error(), "model token-per-minute limit reached; wait at least 31s") || !strings.Contains(err.Error(), "provider-diagnostic-only") {
		t.Fatal("debug error did not retain safe rate-limit guidance and provider diagnostics")
	}
	if strings.Contains(err.Error(), fixtures.RateLimitProviderKey) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatal("debug error did not redact credentials")
	}
}
