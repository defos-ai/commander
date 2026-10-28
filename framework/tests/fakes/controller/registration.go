package fakes

import (
	"context"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/domain/controllers"
)

type RegistrationStore struct {
	controllers.ControllerStore
	observations chan controllers.Controller
	blocked      chan struct{}
	block        atomic.Bool
}

func (s *RegistrationStore) Upsert(ctx context.Context, record controllers.Controller) error {
	if s.block.Load() {
		select {
		case s.blocked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case s.observations <- record:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewRegistrationStore() *RegistrationStore {
	return &RegistrationStore{observations: make(chan controllers.Controller, 100), blocked: make(chan struct{}, 1)}
}
func (s *RegistrationStore) Observations() <-chan controllers.Controller { return s.observations }
func (s *RegistrationStore) BlockHeartbeat()                             { s.block.Store(true) }
func (s *RegistrationStore) Blocked() <-chan struct{}                    { return s.blocked }
