package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/mattn/go-isatty"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (result error) {
	caller := ctx
	config, err := configuration(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.AllInOne {
		fmt.Fprintln(stderr, "Starting local Commander…")
		session, err := local.Start(ctx)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, session.Close()) }()
		ctx = session.Context
		config.MCPURL = session.MCPURL
		config.Notices = session.Notices
		if config.Debug {
			config.Notices = append(config.Notices, "Local host logs: "+session.Directory+"/hosts.log (removed on exit)")
		}
	}
	if file, ok := stdout.(*os.File); !ok || !isatty.IsTerminal(file.Fd()) {
		config.NoColor = true
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := ui.New(ctx, config.Config, stdout)
	defer model.Close()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen())
	_, err = program.Run()
	if config.AllInOne && ctx.Err() != nil && caller.Err() == nil {
		return context.Cause(ctx)
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "commander:", err)
		return 1
	}
	return 0
}
