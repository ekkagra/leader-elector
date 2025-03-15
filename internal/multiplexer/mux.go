package multiplexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mux/internal/state"
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
	Start(ctx context.Context, config <-chan state.Config, in <-chan EventFromReconcile, out chan<- Event)
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

type EventFromReconcile struct {
	Event
}

type EventSourceInfo[T any] struct {
	src        EventSourceInterface[T]
	updateFunc UpdateFunc[T]
	inChan     chan EventFromReconcile
	fwChan     <-chan EventFromReconcile
}

type Mux[T any] struct {
	ctx             context.Context
	configFile      string
	state           *T
	log             *slog.Logger
	muxChan         chan Event
	wg              sync.WaitGroup
	eventSourceInfo map[string]EventSourceInfo[T]
	fanout          *utils.FanOut[state.Config]
	reconciler      ReconcilerInterface[T]
	backoffRequeuer *requeuer[T]
	timedRequeuer   *requeuer[T]
}

func NewMux[T any](ctx context.Context, log *slog.Logger, configFile string, stateInitializer func() *T) *Mux[T] {
	brq := newRequeuer[T]("requeue", time.Second)
	trq := newRequeuer[T]("requeue-after", time.Second)

	m := &Mux[T]{
		ctx:             ctx,
		configFile:      configFile,
		state:           stateInitializer(),
		log:             log,
		muxChan:         make(chan Event, 512),
		wg:              sync.WaitGroup{},
		eventSourceInfo: make(map[string]EventSourceInfo[T]),
		backoffRequeuer: brq,
		timedRequeuer:   trq,
	}

	m.AddEventSource(brq, false)
	m.AddEventSource(trq, false)
	return m
}

func (m *Mux[T]) startEventSource(ctx context.Context, sourceInfo EventSourceInfo[T]) {
	m.wg.Add(1)
	configChan := m.fanout.Add(sourceInfo.src.Name())
	go func() {
		defer func() {
			m.fanout.Delete(sourceInfo.src.Name())
			m.wg.Done()
		}()
		sourceInfo.src.Start(ctx, configChan, sourceInfo.fwChan, m.muxChan)
		m.muxChan <- Event{Name: sourceInfo.src.Name(), Err: ErrEventSourceFinished}
	}()
}

func (m *Mux[T]) waitForAll() {
	go func() {
		defer close(m.muxChan)
		m.wg.Wait()
	}()
}

func (m *Mux[T]) sendEventToEventSource(evSrcName string, data EventFromReconcile) {
	evInfo, ok := m.eventSourceInfo[evSrcName]
	if !ok {
		m.log.Warn("attempt to send data to unknown eventSource", slog.String("evSrcName", evSrcName))
		return
	}

	if evInfo.inChan != nil {
		m.log.Debug("sending notify to event source", slog.String("evSrcName", evSrcName), slog.Any("data", data))
		evInfo.inChan <- data
	} else {
		m.log.Warn("event source doesn't accept inputs from reconciler", slog.String("evSrcName", evSrcName))
	}
}

func (m *Mux[T]) AddEventSource(src EventSourceInterface[T], requiresInput bool) error {
	if src.Name() == "" {
		return fmt.Errorf("event source cannot be empty")
	}

	if _, ok := m.eventSourceInfo[src.Name()]; ok {
		return fmt.Errorf("cannot add event source again: %s", src.Name())
	}

	var (
		inChan chan EventFromReconcile
		fwChan <-chan EventFromReconcile
	)
	if requiresInput {
		inChan = make(chan EventFromReconcile)
		fwChan = utils.Forwarder(m.ctx, utils.ReadChan(inChan))
	}

	m.eventSourceInfo[src.Name()] = EventSourceInfo[T]{
		src:        src,
		updateFunc: src.UpdateFunc(),
		inChan:     inChan,
		fwChan:     fwChan,
	}
	return nil
}

func (m *Mux[T]) SetReconciler(r ReconcilerInterface[T]) {
	m.reconciler = r
}

func (m *Mux[T]) Run() error {
	m.log.Info("Starting mux")

	fileOps := []fsnotify.Op{fsnotify.Create, fsnotify.Rename}
	configChan, err := utils.FileWatcher(m.ctx, m.configFile, fileOps, func(ev fsnotify.Event) state.Config {
		cont, err := os.ReadFile(ev.Name)
		if err != nil {
			return state.Config{}
		}

		cfg := state.Config{AdvertIntv: 1000, EvalIntv: 500}
		if err = json.Unmarshal(cont, &cfg); err != nil {
			return state.Config{}
		}
		return cfg
	})
	if err != nil {
		m.log.Error("unable to start file watcher for config", slog.Any("err", err))
		return err
	}

	m.fanout = utils.NewFanOut(m.ctx, configChan)
	m.fanout.Run()

	for _, evSource := range m.eventSourceInfo {
		m.startEventSource(m.ctx, evSource)
	}

	m.waitForAll()

	for ev := range m.muxChan {
		m.log.Debug("event received", ev.Name, ev.Data)

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

		res, err := m.reconciler.Reconcile(m.ctx, ev.Name, m.state, m.sendEventToEventSource)
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

type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

type ReconcilerInterface[T any] interface {
	Reconcile(ctx context.Context, eventName string, state *T, notifyEventSource func(string, EventFromReconcile)) (Result, error)
}
