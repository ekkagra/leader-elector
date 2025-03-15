package eventsources

import (
	"context"
	m "mux/internal/multiplexer"
	s "mux/internal/state"
)

type A[T any] m.EventSourceInterface[T]

type ConfigUpdater struct {
	m.Named
}

func NewConfigUpdate(name string) *ConfigUpdater {
	return &ConfigUpdater{Named: m.Named{EvName: name}}
}

func (c *ConfigUpdater) Start(ctx context.Context, config <-chan s.Config, _ <-chan m.EventFromReconcile, out chan<- m.Event) {
	for {
		select {
		case <-ctx.Done():
			return

		case cfg := <-config:
			out <- m.Event{Name: c.EvName, Data: cfg}
		}
	}
}

func (c *ConfigUpdater) UpdateFunc() m.UpdateFunc[s.State] {
	return func(data any, state *s.State) error {
		cfg, ok := data.(s.Config)
		if !ok {
			return nil
		}

		state.Config = cfg
		return nil
	}
}
