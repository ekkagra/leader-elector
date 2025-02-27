package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var ErrEventSourceFinished = errors.New("event source finished in mux")

type UpdateFunc[T any] func(data any, state *T) error

type EventSourceInterface[T any] interface {
	Name() string
	Start(ctx context.Context, out chan<- Event)
	UpdateFunc() UpdateFunc[T]
}

type Named struct {
	EvName string
}

func (n *Named) Name() string {
	return n.EvName
}

type NilUpdateEventSource[T any] struct {
	Named
}

func (n *NilUpdateEventSource[T]) UpdateFunc() UpdateFunc[T] {
	return func(data any, state *T) error { return nil }
}

type Event struct {
	Name string
	Data any
	Err  error
}

type EventSourceInfo[T any] struct {
	src        EventSourceInterface[T]
	updateFunc UpdateFunc[T]
}

type Mux[T any] struct {
	state            *T
	log              *slog.Logger
	muxChan          chan Event
	wg               sync.WaitGroup
	eventSourceInfo  map[string]EventSourceInfo[T]
	reconciler       ReconcilerInterface[T]
	clearRequeueChan chan struct{}
}

func NewMux[T any](log *slog.Logger, stateInitializer func() *T) *Mux[T] {
	return &Mux[T]{
		state:            stateInitializer(),
		log:              log,
		muxChan:          make(chan Event, 128),
		wg:               sync.WaitGroup{},
		eventSourceInfo:  make(map[string]EventSourceInfo[T]),
		clearRequeueChan: nil,
	}
}

func (m *Mux[T]) startEventSource(ctx context.Context, source EventSourceInterface[T]) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		source.Start(ctx, m.muxChan)
		m.muxChan <- Event{Name: source.Name(), Err: ErrEventSourceFinished}
	}()
}

func (m *Mux[T]) waitForAll() {
	go func() {
		defer close(m.muxChan)
		m.wg.Wait()
	}()
}

func (m *Mux[T]) AddEventSource(src EventSourceInterface[T]) error {
	if src.Name() == "" {
		return fmt.Errorf("event source cannot be empty")
	}

	if _, ok := m.eventSourceInfo[src.Name()]; ok {
		return fmt.Errorf("cannot add event source again: %s", src.Name())
	}

	m.eventSourceInfo[src.Name()] = EventSourceInfo[T]{
		src:        src,
		updateFunc: src.UpdateFunc(),
	}
	return nil
}

func (m *Mux[T]) SetReconciler(r ReconcilerInterface[T]) {
	m.reconciler = r
}

func (m *Mux[T]) Run(ctx context.Context) {
	m.log.Info("Starting mux")

	for _, evSource := range m.eventSourceInfo {
		m.startEventSource(ctx, evSource.src)
	}

	m.waitForAll()

	for ev := range m.muxChan {
		m.log.Info("event received", ev.Name, ev.Data)

		if ev.Name != "requeue" && ev.Name != "requeue-after" {
			src, ok := m.eventSourceInfo[ev.Name]
			if !ok {
				m.log.Warn("unknown event source", slog.String("name", ev.Name))
				continue
			}

			if err := src.updateFunc(ev.Data, m.state); err != nil {
				m.log.Error("unable to update state with events updateFunc", slog.String("event", ev.Name), slog.Any("data", ev.Data))
				continue
			}
		} else if ev.Name == "requeue" {
			m.resetRequeueChan()
		}

		res, err := m.reconciler.Reconcile(ctx, ev.Name, m.state)
		if err != nil {
			m.log.Error("error on reconciling", slog.String("event", ev.Name), slog.Any("error", err))
			res.Requeue = true
		}

		if res.Requeue || res.RequeueAfter > 0 {
			if res.RequeueAfter > 0 {
				m.requeue(ctx, "requeue-after", res.RequeueAfter, false)
				continue
			}

			if err != nil {
				m.resetRequeueChan()
				m.requeue(ctx, "requeue", time.Second*2, true)
				continue
			}

			if m.clearRequeueChan != nil {
				continue
			}
			m.requeue(ctx, "requeue", time.Second*2, true)
		}
		m.resetRequeueChan()
	}
}

func (m *Mux[T]) resetRequeueChan() {
	if m.clearRequeueChan != nil {
		close(m.clearRequeueChan)
		m.clearRequeueChan = nil
	}
}

func (m *Mux[T]) requeue(ctx context.Context, name string, delay time.Duration, cancellable bool) {
	var clearChan chan struct{}
	if cancellable {
		m.clearRequeueChan = make(chan struct{})
		clearChan = m.clearRequeueChan
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			m.muxChan <- Event{Name: name}
		case <-clearChan:
			return
		}
	}()
}

type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

type ReconcilerInterface[T any] interface {
	Reconcile(context.Context, string, *T) (Result, error)
}
