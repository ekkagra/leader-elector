package multiplexer

import (
	"context"
	"mux/internal/state"
	"time"
)

type requeuer[T any] struct {
	NilUpdateEventSource[T]
	ticker *time.Ticker
}

func newRequeuer[T any](name string, duration time.Duration) *requeuer[T] {
	t := time.NewTicker(duration)
	t.Stop()

	return &requeuer[T]{
		NilUpdateEventSource: NilUpdateEventSource[T]{Named{name}},
		ticker:               t,
	}
}

func (r *requeuer[T]) Start(ctx context.Context, _ <-chan state.Config, _ <-chan EventFromReconcile, out chan<- Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.ticker.C:
			out <- Event{Name: r.EvName}
		}
	}
}
