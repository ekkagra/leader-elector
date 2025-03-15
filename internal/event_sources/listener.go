package eventsources

import (
	"context"
	"errors"
	"log/slog"
	m "mux/internal/multiplexer"
	pt "mux/internal/packet"
	"mux/internal/state"
	"net"
	"net/netip"
	"sync"
)

type Listener struct {
	m.Named
	log *slog.Logger

	listenAddr *state.Addr
	listener   *net.UDPConn

	wg sync.WaitGroup
}

func NewListener(log *slog.Logger, name string) *Listener {
	return &Listener{
		Named: m.Named{EvName: name},
		log:   log,
		wg:    sync.WaitGroup{},
	}
}

func (l *Listener) Start(ctx context.Context, config <-chan state.Config, _ <-chan m.Event, out chan<- m.Event) {
	defer l.stopListener()

	for {
		select {
		case <-ctx.Done():
			return
		case cfg := <-config:
			if cfg.Id == 0 && cfg.AdvertIntv == 0 {
				l.log.Error("incomplete cfg", slog.Any("cfg", cfg))
				continue
			}
			l.log.Info("cfg", "cfg", cfg)

			if l.listenAddr == nil {
				if err := l.startListener(&cfg, out); err != nil {
					l.log.Error("unable to start listener", slog.Any("err", err))
				}
				continue
			}

			if cfg.ListenAddr == *l.listenAddr {
				continue
			}

			l.stopListener()
			l.startListener(&cfg, out)
		}
	}

}

func (l *Listener) UpdateFunc() m.UpdateFunc[state.State] {
	return func(data any, state *state.State) error {
		pkt, ok := data.(pt.PacketRx)
		if !ok {
			return errors.New("invalid data")
		}

		src, _ := netip.ParseAddrPort(pkt.Src)
		state.PacketRecvMap[src] = pkt
		return nil
	}
}

func (l *Listener) startListener(cfg *state.Config, outChan chan<- m.Event) error {
	laddr := &net.UDPAddr{IP: cfg.ListenAddr.IP.AsSlice(), Port: cfg.ListenAddr.Port}
	ln, err := net.ListenUDP("udp", laddr)
	if err != nil {
		l.log.Error("unable to listen udp", slog.Any("laddr", laddr), slog.Any("err", err))
		return err
	}

	l.log.Info("listening udp on", slog.Any("laddr", laddr))
	l.listener = ln
	l.listenAddr = &cfg.ListenAddr

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.readForever(outChan)
		l.log.Info("finished reading from udp conn")
	}()
	return nil
}

func (l *Listener) stopListener() {
	if l.listener != nil {
		l.listener.Close()
		l.wg.Wait()
		l.listener = nil
		l.listenAddr = nil
	}
}

func (l *Listener) readForever(outChan chan<- m.Event) {
	for {
		var data [1024]byte
		n, remote, err := l.listener.ReadFromUDP(data[0:])
		if err != nil {
			l.log.Error("error at read", slog.Any("err", err))
			break
		}
		l.log.Debug("rx data", slog.Any("remote", remote.String()), slog.Any("data", string(data[:n])))

		packet := pt.PacketRx{Src: remote.AddrPort().String()}
		if err := packet.UnmarshalWithTime(data[:n]); err != nil {
			l.log.Error("unable to unmarshal packet", slog.Any("err", err))
			continue
		}

		l.log.Debug("got packet", slog.Any("packet", packet))
		outChan <- m.Event{Name: l.EvName, Data: packet}
	}
}
