package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
)

func AnswerAsOperator(ctx context.Context, operationID, questionID, answer string) (operations.Operation, error) {
	endpoint := os.Getenv("COMMANDER_TEST_API_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:14274"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return operations.Operation{}, errors.New("operator answer requires a valid test API URL")
	}
	username := os.Getenv("COMMANDER_TEST_API_USERNAME")
	if username == "" {
		username = "api-fixture-user"
	}
	password := os.Getenv("COMMANDER_TEST_API_PASSWORD")
	if password == "" {
		password = "api-fixture-password"
	}
	body, err := json.Marshal(Object{"answer": answer})
	if err != nil {
		return operations.Operation{}, errors.New("encode operator answer")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	path := strings.TrimRight(endpoint, "/") + "/api/v1/operations/" + url.PathEscape(operationID) + "/questions/" + url.PathEscape(questionID) + "/answer"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return operations.Operation{}, errors.New("create operator answer request")
	}
	request.SetBasicAuth(username, password)
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return operations.Operation{}, errors.New("operator answer request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return operations.Operation{}, fmt.Errorf("operator answer returned HTTP %d", response.StatusCode)
	}
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	var operation operations.Operation
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &operation) != nil || operation.ID != operationID {
		return operations.Operation{}, errors.New("operator answer returned an invalid operation")
	}
	return operation, nil
}
