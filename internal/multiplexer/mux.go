package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mux/internal/utils"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var ErrEventSourceFinished = errors.New("event source finished in mux")

type UpdateFunc[T any] func(data any, state *T) error

type EventSourceInterface[T any] interface {
	Name() string
	Start(ctx context.Context, config <-chan string, out chan<- Event)
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
	configFile      string
	state           *T
	log             *slog.Logger
	muxChan         chan Event
	wg              sync.WaitGroup
	eventSourceInfo map[string]EventSourceInfo[T]
	fanout          *utils.FanOut[string]
	reconciler      ReconcilerInterface[T]
	backoffRequeuer *requeuer[T]
	timedRequeuer   *requeuer[T]
}

func NewMux[T any](log *slog.Logger, configFile string, stateInitializer func() *T) *Mux[T] {
	brq := newRequeuer[T]("requeue", time.Second)
	trq := newRequeuer[T]("requeue-after", time.Second)

	m := &Mux[T]{
		configFile:      configFile,
		state:           stateInitializer(),
		log:             log,
		muxChan:         make(chan Event, 128),
		wg:              sync.WaitGroup{},
		eventSourceInfo: make(map[string]EventSourceInfo[T]),
		backoffRequeuer: brq,
		timedRequeuer:   trq,
	}

	m.AddEventSource(brq)
	m.AddEventSource(trq)
	return m
}

func (m *Mux[T]) startEventSource(ctx context.Context, source EventSourceInterface[T]) {
	m.wg.Add(1)
	configChan := m.fanout.Add(source.Name())
	go func() {
		defer func() {
			m.fanout.Delete(source.Name())
			m.wg.Done()
		}()
		source.Start(ctx, configChan, m.muxChan)
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

func (m *Mux[T]) Run(ctx context.Context) error {
	m.log.Info("Starting mux")

	fileOps := []fsnotify.Op{fsnotify.Create, fsnotify.Rename}
	configChan, err := utils.FileWatcher(ctx, m.configFile, fileOps, func(ev fsnotify.Event) string {
		cont, _ := os.ReadFile(ev.Name)
		return string(cont)
	})
	if err != nil {
		m.log.Error("unable to start file watcher for config", slog.Any("err", err))
		return err
	}

	m.fanout = utils.NewFanOut(ctx, configChan)
	m.fanout.Run()

	for _, evSource := range m.eventSourceInfo {
		m.startEventSource(ctx, evSource.src)
	}

	m.waitForAll()

	for ev := range m.muxChan {
		m.log.Info("event received", ev.Name, ev.Data)

		m.backoffRequeuer.ticker.Stop()
		if ev.Name == "requeue-after" {
			m.timedRequeuer.ticker.Stop()
		}

		src, ok := m.eventSourceInfo[ev.Name]
		if !ok {
			m.log.Warn("unknown event source", slog.String("name", ev.Name))
			continue
		}

		if ev.Err != nil {
			m.log.Error("received err for event", slog.String("event", ev.Name), slog.Any("err", ev.Err))
			continue
		}

		if err := src.updateFunc(ev.Data, m.state); err != nil {
			m.log.Error("unable to update state with events updateFunc", slog.String("event", ev.Name), slog.Any("data", ev.Data))
			continue
		}

		res, err := m.reconciler.Reconcile(ctx, ev.Name, m.state)
		if err != nil {
			m.log.Error("error on reconciling", slog.String("event", ev.Name), slog.Any("error", err))
			res.Requeue = true
		}

		if res.RequeueAfter > 0 {
			m.timedRequeuer.ticker.Reset(res.RequeueAfter)
			continue
		}

		if res.Requeue {
			m.backoffRequeuer.ticker.Reset(time.Second)
		}
	}
	return nil
}

// ------------------------------------
// Requeuer
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

func (r *requeuer[T]) Start(ctx context.Context, _ <-chan string, out chan<- Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.ticker.C:
			out <- Event{Name: r.EvName}
		}
	}
}

type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

type ReconcilerInterface[T any] interface {
	Reconcile(context.Context, string, *T) (Result, error)
}
