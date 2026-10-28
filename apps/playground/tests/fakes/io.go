package fakes

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

type TerminalOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *TerminalOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *TerminalOutput) String() string {
	b.mu.Lock()
	output := b.buffer.String()
	b.mu.Unlock()
	return ansi.Strip(output)
}

type CredentialCommandInput struct{ t *testing.T }

func NewCredentialCommandInput(t *testing.T) CredentialCommandInput {
	return CredentialCommandInput{t: t}
}

func (r CredentialCommandInput) Read([]byte) (int, error) {
	r.t.Error("credential command attempted to read interactive input")
	return 0, io.EOF
}

type MCPAuthTransport string

func (token MCPAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+string(token))
	return http.DefaultTransport.RoundTrip(request)
}
