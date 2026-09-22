// Package cli hosts the interactive Commander playground.
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

	"commander/apps/playground/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
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
	if file, ok := stdout.(*os.File); !ok || !isatty.IsTerminal(file.Fd()) {
		config.NoColor = true
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := ui.New(ctx, config, stdout)
	defer model.Close()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen())
	_, err = program.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "commander:", err)
		return 1
	}
	return 0
}
