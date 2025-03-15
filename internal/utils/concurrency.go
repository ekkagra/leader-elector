package utils

import (
	"context"
	"log/slog"
	"os"
)

var (
	log = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("utils", "concurrency")
)

func forwarder[T any](ctx context.Context, in <-chan T, out chan T) {
	var txChan chan T
	var local T
	for {
		select {
		case val, open := <-in:
			log.Debug("Received in", slog.Any("val", val), slog.Any("open", open))
			if !open {
				in = nil
				continue
			}

			local = val
			txChan = out
			log.Debug("Enabled out", slog.Any("val", val), slog.Any("open", open))
		case txChan <- local:
			log.Debug("Sent local to txChan", slog.Any("local", local))
			txChan = nil
		case <-ctx.Done():
			return
		}

		if in == nil && txChan == nil {
			return
		}
	}
}

func Forwarder[T any](ctx context.Context, in <-chan T) <-chan T {
	out := make(chan T)

	started := make(chan struct{})
	go func() {
		defer close(out)
		close(started)
		forwarder(ctx, in, out)
	}()
	<-started
	return out
}

type forwarderInfo[T any] struct {
	name    string
	inChan  chan T
	outChan chan T
	ctx     context.Context
	cancel  context.CancelFunc
}

type updateEventType int8

const (
	addEvent updateEventType = iota
	delEvent
)

type updateEvent[T any] struct {
	forwarderInfo *forwarderInfo[T]
	eventType     updateEventType
}

type FanOut[T any] struct {
	ctx          context.Context
	inChan       <-chan T
	updateChan   chan updateEvent[T]
	forwarderMap map[string]*forwarderInfo[T]
}

func NewFanOut[T any](ctx context.Context, inChan <-chan T) *FanOut[T] {
	return &FanOut[T]{
		ctx:          ctx,
		inChan:       inChan,
		updateChan:   make(chan updateEvent[T], 128),
		forwarderMap: make(map[string]*forwarderInfo[T]),
	}
}

func (f *FanOut[T]) Add(name string) <-chan T {
	ctx, cancel := context.WithCancel(f.ctx)

	event := updateEvent[T]{
		forwarderInfo: &forwarderInfo[T]{
			name:    name,
			inChan:  make(chan T),
			outChan: make(chan T),
			ctx:     ctx,
			cancel:  cancel},
		eventType: addEvent,
	}

	f.updateChan <- event
	return event.forwarderInfo.outChan
}

func (f *FanOut[T]) Delete(name string) {
	event := updateEvent[T]{
		forwarderInfo: &forwarderInfo[T]{name: name},
		eventType:     delEvent,
	}
	f.updateChan <- event
}

func (f *FanOut[T]) run() {
	defer func() {
		for name, out := range f.forwarderMap {
			if out.cancel != nil {
				out.cancel()
			}
			delete(f.forwarderMap, name)
		}
	}()

	var val *T
	input := Forwarder(f.ctx, f.inChan)
	for {
		select {
		case <-f.ctx.Done():
			return
		case value, open := <-input:
			val = &value
			if !open {
				return
			}
			for _, out := range f.forwarderMap {
				out.inChan <- *val
			}
		case ev := <-f.updateChan:
			switch ev.eventType {
			case addEvent:
				f.forwarderMap[ev.forwarderInfo.name] = ev.forwarderInfo
				go func() {
					defer close(ev.forwarderInfo.outChan)
					forwarder(ev.forwarderInfo.ctx, ev.forwarderInfo.inChan, ev.forwarderInfo.outChan)
				}()
				if val != nil {
					ev.forwarderInfo.inChan <- *val
				}
			case delEvent:
				outChanInfo, ok := f.forwarderMap[ev.forwarderInfo.name]
				if ok && outChanInfo.cancel != nil {
					outChanInfo.cancel()
				}
				delete(f.forwarderMap, ev.forwarderInfo.name)
			}
		}
	}
}

func (f *FanOut[T]) Run() {
	started := make(chan struct{})
	go func() {
		started <- struct{}{}
		f.run()
	}()
	<-started
}

func ReadChan[T any](inp chan T) <-chan T {
	return inp
}
