//go:build linux

package fakes

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// A downloaded copy of the test executable provides a deterministic subprocess
// fixture without shell dependencies or public release downloads.
func RunProcessFixture() {
	name := filepath.Base(os.Args[0])
	if name != "process-agent-exit" && name != "process-agent-wait" {
		return
	}
	go func() {
		time.Sleep(15 * time.Second)
		os.Exit(1)
	}()
	storage := os.Getenv("STORAGE_URL")
	if len(os.Args) == 2 && os.Args[1] == "descendant" {
		signal.Ignore(syscall.SIGTERM)
		if err := os.WriteFile(filepath.Join(storage, "descendant.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(1)
		}
		select {}
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		os.Exit(1)
	}
	termination := make(chan os.Signal, 1)
	signal.Notify(termination, syscall.SIGTERM)
	child := exec.Command(os.Args[0], "descendant")
	child.Env = os.Environ()
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(filepath.Join(storage, "descendant.pid")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if name == "process-agent-wait" {
		<-termination
	}
	// Deliberately exit before our child: the spawner must kill descendants
	// even when its direct process is already gone or exits first on SIGTERM.
	os.Exit(0)
}
