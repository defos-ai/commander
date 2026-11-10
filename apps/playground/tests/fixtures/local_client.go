package fixtures

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

type LocalPlayground struct {
	Output *fakes.HeadlessOutput
	Errors *fakes.HeadlessOutput
	input  *io.PipeWriter
	reader *io.PipeReader
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func StartAttachmentModel(t *testing.T) (string, *fakes.AttachmentModel) {
	t.Helper()
	model := &fakes.AttachmentModel{}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	return server.URL, model
}

func StartLocalPlayground(t *testing.T, directory, modelURL string, extra ...string) *LocalPlayground {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reader, input := io.Pipe()
	playground := &LocalPlayground{Output: &fakes.HeadlessOutput{}, Errors: &fakes.HeadlessOutput{}, input: input, reader: reader, cancel: cancel, done: make(chan struct{})}
	args := append([]string{"--state-dir", directory, "--provider=other", "--model=fixture-model", "--base-url=" + modelURL, "--no-color"}, extra...)
	go func() {
		defer close(playground.done)
		playground.err = cli.Run(ctx, args, reader, playground.Output, playground.Errors)
	}()
	t.Cleanup(func() {
		if err := playground.Stop(); err != nil {
			t.Errorf("stop attached Playground: %v", err)
		}
	})
	return playground
}

func (p *LocalPlayground) Send(ctx context.Context, input string) error {
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(p.input, input)
		written <- err
	}()
	select {
	case err := <-written:
		return err
	case <-p.done:
		return errors.New("Playground exited before reading input")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *LocalPlayground) Await(ctx context.Context, text string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(ansi.Strip(p.Output.String()), text) {
			return nil
		}
		select {
		case <-p.done:
			return fmt.Errorf("Playground exited before showing %q: %w", text, errors.Join(p.err, errors.New(p.Errors.String())))
		case <-ctx.Done():
			return fmt.Errorf("waiting for Playground %q: %w", text, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *LocalPlayground) Stop() error {
	p.cancel()
	_ = p.input.Close()
	_ = p.reader.Close()
	select {
	case <-p.done:
		return p.err
	case <-time.After(10 * time.Second):
		return errors.New("Playground did not finish client shutdown")
	}
}
