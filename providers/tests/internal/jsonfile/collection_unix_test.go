//go:build unix && !aix

package jsonfile_test

import (
	"path/filepath"
	"testing"
	"time"

	"commander/providers/internal/jsonfile"

	"golang.org/x/sys/unix"
)

func TestFIFOPathsFailWithoutBlocking(t *testing.T) {
	for _, lockPath := range []bool{false, true} {
		// Arrange.
		path := filepath.Join(t.TempDir(), "records.json")
		fifo := path
		if lockPath {
			fifo += ".lock"
		}
		if err := unix.Mkfifo(fifo, 0600); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)

		// Act.
		go func() { _, err := jsonfile.New(path); finished <- err }()

		// Assert.
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("FIFO path was accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO path blocked collection construction")
		}
	}
}
