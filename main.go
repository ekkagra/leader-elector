package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"mux/internal/multiplexer"
	"mux/internal/state"
	"os"
	"time"
)

func main() {

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("main started")
	defer log.Info("main finished")

	var configFile string
	flag.StringVar(&configFile, "configFile", "./config.json", "Config file path")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(time.Second * 30)
		cancel()
	}()

	mux := multiplexer.NewMux(log.With("comp", "mux"), configFile, func() *state.State {
		return &state.State{}
	})

	count := 1
	for i := range count {
		mux.AddEventSource(multiplexer.NewFileEventSource(fmt.Sprintf("file-change-%d", i), log.With("evSource", "file-change"), i+1))
	}

	mux.SetReconciler(&multiplexer.Reconciler{Log: log.With("comp", "reconciler")})

	if err := mux.Run(ctx); err != nil {
		os.Exit(1)
	}
}
