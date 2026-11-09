package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type rateLimitError struct {
	err   error
	delay time.Duration
}

func (e *rateLimitError) Error() string { return e.err.Error() }
func (e *rateLimitError) Unwrap() error { return e.err }

func (c *Client) responsesWithRetry(ctx context.Context, system string, history []Message, tools []Tool, delta func(string)) (Message, error) {
	var waited time.Duration
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return Message{}, err
		}
		result, err := c.responses(ctx, system, history, tools, delta)
		if err != nil && ctx.Err() != nil {
			return Message{}, ctx.Err()
		}
		var limit *rateLimitError
		if err == nil || attempt == 2 || !errors.As(err, &limit) || limit.delay > 30*time.Second-waited {
			return result, err
		}
		timer := time.NewTimer(limit.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Message{}, ctx.Err()
		case <-timer.C:
			waited += limit.delay
		}
	}
}

func (c *Client) responseError(summary string, body []byte, headers http.Header, retry bool) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	detail := body
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) != 0 {
		detail = envelope.Error
	}
	var failure struct {
		Code    string            `json:"code"`
		Type    string            `json:"type"`
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(detail, &failure) != nil || failure.Code != "rate_limit_exceeded" {
		return c.diagnosticError(summary, body)
	}
	summary = "model rate limit reached"
	if failure.Type == "tokens" {
		summary = "model token-per-minute limit reached"
	}
	combined := headers.Clone()
	if combined == nil {
		combined = make(http.Header)
	}
	for name, value := range failure.Headers {
		combined.Add(name, value)
	}
	delay, advised := rateLimitDelay(combined)
	if advised {
		summary += fmt.Sprintf("; wait at least %.0fs before trying again", math.Ceil(delay.Seconds()))
	} else {
		summary += "; try again later"
	}
	err := c.diagnosticError(summary, body)
	if retry && advised {
		return &rateLimitError{err: err, delay: delay}
	}
	return err
}

func rateLimitDelay(headers http.Header) (time.Duration, bool) {
	var longest time.Duration
	found := false
	for _, name := range []string{"Retry-After", "Retry-After-Ms"} {
		for _, value := range headers.Values(name) {
			value = strings.TrimSpace(value)
			unit := "s"
			if name == "Retry-After-Ms" {
				unit = "ms"
			}
			var delay time.Duration
			number, err := strconv.ParseFloat(value, 64)
			if err == nil && (math.IsNaN(number) || math.IsInf(number, 0)) {
				continue
			}
			if err == nil || errors.Is(err, strconv.ErrRange) && math.IsInf(number, 1) {
				scale := time.Second
				if unit == "ms" {
					scale = time.Millisecond
				}
				if number >= float64(math.MaxInt64)/float64(scale) {
					// An excessive valid delay must not yield to a shorter hint.
					delay, err = time.Duration(math.MaxInt64), nil
				} else {
					delay, err = time.ParseDuration(value + unit)
				}
			} else if name == "Retry-After" {
				var until time.Time
				until, err = http.ParseTime(value)
				if err == nil {
					delay = max(0, time.Until(until))
				}
			}
			if err == nil && delay >= 0 {
				longest = max(longest, delay)
				found = true
			}
		}
	}
	return longest, found
}
