package fakes

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

type TerminalOutput struct {
	buffer bytes.Buffer
}

func (b *TerminalOutput) Write(p []byte) (int, error) {
	return b.buffer.Write(p)
}

func (b *TerminalOutput) String() string {
	output := b.buffer.String()
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
