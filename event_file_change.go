package main

import (
	"context"
	"fmt"
	"time"
)

type FileEventSource struct {
	Named
	delay int
}

func NewFileEventSource(name string, delay int) *FileEventSource {
	return &FileEventSource{
		Named: Named{EvName: name},
		delay: delay,
	}
}

func (f *FileEventSource) Start(ctx context.Context, outChan chan<- Event) {
	ticker := time.NewTicker(time.Second * time.Duration(f.delay))
	count := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			outChan <- Event{Name: f.EvName, Data: count}
			count += 1
		}
	}
}

func (f *FileEventSource) UpdateFunc() UpdateFunc[State] {
	return func(data any, state *State) error {
		val, ok := data.(int)
		if !ok {
			return fmt.Errorf("unable to cast to int")
		}

		state.fileState = val
		return nil
	}
}
