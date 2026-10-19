package native

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/comms"
)

type child struct {
	command          *exec.Cmd
	done             chan struct{}
	download         string
	result           runtimes.Result
	registrationDone chan struct{}
	registrationErr  error
	subscription     comms.Subscription
	processMu        sync.Mutex
	reaped           bool
	cleanupErr       error
}

func (c *child) wait() {
	c.cleanupErr = waitProcess(c)
	close(c.done)
}

func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *child) stop() (result error) {
	if c.subscription != nil {
		if err := c.subscription.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
			result = err
		}
		c.subscription = nil
	}
	select {
	case <-c.registrationDone:
	case <-time.After(5 * time.Second):
		result = errors.Join(result, errors.New("registration observer did not stop"))
	}
	if !c.exited() {
		if err := terminate(c); err != nil && !c.exited() {
			// Still attempt a forced stop below; never skip Wait on signal failure.
			_ = kill(c)
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-c.done:
			timer.Stop()
		case <-timer.C:
			_ = kill(c)
			timer.Reset(5 * time.Second)
			select {
			case <-c.done:
				timer.Stop()
			case <-timer.C:
				return errors.Join(result, fmt.Errorf("agent %s did not exit; shared state must be retained", c.result.AgentID))
			}
		}
	}
	if c.cleanupErr != nil {
		return errors.Join(result, c.cleanupErr)
	}
	return errors.Join(result, os.RemoveAll(c.download))
}
