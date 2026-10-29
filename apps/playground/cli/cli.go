package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/adminhost"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (result error) {
	if len(args) != 0 && args[0] == "credentials" {
		if err := ctx.Err(); err != nil {
			return err
		}
		return showCredentials(args[1:], stdout)
	}
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
	profile, err := config.openState()
	if err != nil {
		return err
	}
	if profile != nil {
		defer func() { result = errors.Join(result, profile.Close()) }()
	}
	if config.AllInOne {
		fmt.Fprintln(stderr, "Starting local Commander…")
		options := local.Options{ServeAdmin: config.ServeAdminUI}
		if profile != nil {
			options.Directory = filepath.Join(profile.Directory, "local")
		}
		session, err := local.StartWithOptions(ctx, options)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, session.Close()) }()
		ctx = session.Context
		config.configureLocalSession(session)
	} else {
		admin := adminhost.New(ctx, io.Discard, "")
		defer func() { result = errors.Join(result, admin.Close()) }()
		config.configureAdminServer(admin.Start)
		if config.ServeAdminUI {
			address, err := config.ServeAdmin(ctx, nil)
			if err != nil {
				config.AdminURL = ""
				config.AdminHint = "Admin UI unavailable. Use /admin serve to try again."
				config.Notices = append(config.Notices, config.AdminHint)
			} else {
				config.Config.SetAdminURL(address)
			}
		}
	}
	if file, ok := stdout.(*os.File); !ok || !isatty.IsTerminal(file.Fd()) {
		config.NoColor = true
	} else {
		restore, err := termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(file))
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, restore()) }()
		// Alternate scrolling sends wheel/trackpad movement as arrow keys,
		// leaving drag selection and copying with the terminal. Restore its mode
		// on exit so the surrounding shell keeps its original scroll behavior.
		defer func() {
			_, err := io.WriteString(stdout, "\x1b[?1007r")
			result = errors.Join(result, err)
		}()
		if _, err := io.WriteString(stdout, "\x1b[?1007s\x1b[?1007h"); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := ui.New(ctx, config.Config, stdout)
	defer func() { result = errors.Join(result, model.Close()) }()
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
