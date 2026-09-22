package cli_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"commander/apps/playground/cli"
	"golang.org/x/sys/unix"
)

func TestCommanderTerminalDoesNotQueryColorsWhileReadingInput(t *testing.T) {
	// Arrange: run the real CLI in a terminal that answers color/cursor queries.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 28, Col: 100}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommanderTerminalProcess$")
	child.Env = append(os.Environ(), "COMMANDER_TEST_TERMINAL=1", "TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=")
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	out := new(output)
	var lateQueries atomic.Int32
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		query := regexp.MustCompile("\x1b\\]11;\\?\x1b\\\\|\x1b\\[6n")
		var pending string
		data := make([]byte, 4096)
		for {
			n, err := master.Read(data)
			if err != nil {
				return
			}
			_, _ = out.Write(data[:n])
			pending += string(data[:n])
			matches := query.FindAllStringIndex(pending, -1)
			for _, match := range matches {
				if strings.Contains(out.String(), "Select your model provider") {
					lateQueries.Add(1)
				}
				response := "\x1b[13;1R"
				if strings.Contains(pending[match[0]:match[1]], "]11;") {
					response = "\x1b]11;rgb:1919/1a1a/1b1b\x1b\\"
				}
				_, _ = master.WriteString(response)
			}
			if len(matches) > 0 {
				pending = pending[matches[len(matches)-1][1]:]
			}
			// Retain only a possible partial query between reads.
			pending = pending[max(0, len(pending)-8):]
		}
	}()
	defer func() {
		_ = child.Process.Kill()
		_ = master.Close()
		<-readDone
	}()
	until(t, out, "Select your model provider")

	// Act: help renders adaptive text colors for the first time during input.
	press(t, master, "/help\r")
	until(t, out, "Use ↑/↓ and Enter")
	press(t, master, "/exit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("terminal session did not exit")
	}

	// Assert: detection never steals keyboard input or inserts terminal replies.
	if lateQueries.Load() != 0 {
		t.Fatalf("sent %d terminal queries while reading user input", lateQueries.Load())
	}
	if strings.Contains(out.String(), "]11;rgb:") || strings.Contains(out.String(), "[13;1R") {
		t.Fatal("terminal response leaked into the interface")
	}
}

func TestCommanderTerminalProcess(t *testing.T) {
	if os.Getenv("COMMANDER_TEST_TERMINAL") != "1" {
		return
	}
	for _, name := range []string{"COMMANDER_DEBUG", "COMMANDER_LLM_PROVIDER", "COMMANDER_LLM_MODEL", "COMMANDER_LLM_BASE_URL", "COMMANDER_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY", "COMMANDER_MCP_URL"} {
		t.Setenv(name, "")
	}
	if err := cli.Run(t.Context(), []string{"--no-color=false", "--mcp-url=http://localhost:4273/mcp"}, os.Stdin, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}
