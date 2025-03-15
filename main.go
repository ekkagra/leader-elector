package main

import (
	"context"
	"flag"
	"log/slog"
	eventsources "mux/internal/event_sources"
	"mux/internal/multiplexer"
	"mux/internal/packet"
	"mux/internal/reconciler"
	"mux/internal/state"
	"mux/internal/utils"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
)

func setupSignalHandler() context.Context {
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, syscall.SIGTERM, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-exit
		cancel()
	}()

	return ctx
}

func main() {

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("main started")
	defer log.Info("main finished")

	var configFile string
	flag.StringVar(&configFile, "configFile", "./config.json", "Config file path")
	flag.Parse()

	mux := multiplexer.NewMux(setupSignalHandler(), log.With("comp", "mux"), configFile,
		func() *state.State {
			return &state.State{
				HAState:       state.BackupState,
				PacketRecvMap: make(map[netip.Addr]packet.PacketRx),
				PacketSentMap: make(map[netip.Addr]*utils.RingBuffer[packet.PacketTx]),
				Config: state.Config{
					AdvertIntv: 500,
					EvalIntv:   100,
				},
			}
		})

	mux.AddEventSource(eventsources.NewConfigUpdate("config-updater"), false)

	mux.AddEventSource(eventsources.NewListener(log.With("evSource", "listener"), "packet-listener"), false)

	mux.AddEventSource(eventsources.NewPacketSender(log.With("evSoure", "packet-sender"), "packet-sender"), true)

	mux.SetReconciler(reconciler.NewReconciler(log.With("comp", "reconciler")))

	if err := mux.Run(); err != nil {
		os.Exit(1)
	}
}
