package eventsources

import (
	"context"
	"log/slog"
	m "mux/internal/multiplexer"
	"mux/internal/state"
)

type PacketSender struct {
	m.Named
	log *slog.Logger
}

func NewPacketSender(log *slog.Logger, name string) *PacketSender {
	return &PacketSender{
		Named: m.Named{EvName: name},
		log:   log,
	}
}

func (*PacketSender) Start(ctx context.Context, config <-chan string, out chan<- m.Event) {
	for {
		select {
		case <-ctx.Done():
			return

		}
	}

}

func (*PacketSender) UpdateFunc() m.UpdateFunc[state.State] {
	return nil
}
