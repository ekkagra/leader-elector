package multiplexer

import (
	"context"
	"fmt"
	"log/slog"
	"mux/internal/state"
	"strconv"
	"time"
)

type FileEventSource struct {
	Named
	delay int
	log   *slog.Logger
}

func NewFileEventSource(name string, log *slog.Logger, delay int) *FileEventSource {
	return &FileEventSource{
		Named: Named{EvName: name},
		delay: delay,
		log:   log,
	}
}

func (f *FileEventSource) Start(ctx context.Context, configChan <-chan string, outChan chan<- Event) {
	ticker := time.NewTicker(time.Second * time.Duration(f.delay))
	count := 0
	for {
		select {
		case <-ctx.Done():
			return

		case config := <-configChan:
			f.log.Info("rx configChan event", slog.Any("config", config))
			if delay, err := strconv.Atoi(config); err == nil {
				ticker.Reset(time.Second * time.Duration(delay))
				f.log.Info("ticker reset to", slog.Any("delay", delay))
			} else {
				f.log.Info("strconv error", slog.Any("err", err))
			}
		case <-ticker.C:
			outChan <- Event{Name: f.EvName, Data: count}
			count += 1
		}
	}
}

func (f *FileEventSource) UpdateFunc() UpdateFunc[state.State] {
	return func(data any, state *state.State) error {
		_, ok := data.(int)
		if !ok {
			return fmt.Errorf("unable to cast to int")
		}

		return nil
	}
}
