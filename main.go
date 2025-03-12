package main

import (
	"context"
	"flag"
	"log/slog"
	eventsources "mux/internal/event_sources"
	"mux/internal/multiplexer"
	"mux/internal/state"
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

	mux := multiplexer.NewMux(log.With("comp", "mux"), configFile, func() *state.State {
		return &state.State{}
	})

	mux.AddEventSource(eventsources.NewListener(log.With("evSource", "listener"), "packet-listener"))

	mux.SetReconciler(&multiplexer.Reconciler{Log: log.With("comp", "reconciler")})

	if err := mux.Run(setupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
