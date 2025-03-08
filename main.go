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

	var conn bool
	var selfIPPort string
	var remoteIPPort string
	flag.StringVar(&selfIPPort, "selfIPPort", "", "selfIPPort")
	flag.StringVar(&remoteIPPort, "remoteIPPort", "127.0.0.4:5000", "remoteIPPort")
	flag.BoolVar(&conn, "conn", false, "connection oriented")
	flag.Parse()

	// if conn {
	// 	packet.GenerateConn(log, selfIPPort, remoteIPPort)
	// } else {
	// 	packet.GenerateNoConn(log, selfIPPort, remoteIPPort)
	// }

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(time.Second * 30)
		cancel()
	}()

	mux := multiplexer.NewMux(log.With("comp", "mux"), func() *state.State {
		return &state.State{}
	})

	count := 1
	for i := range count {
		mux.AddEventSource(multiplexer.NewFileEventSource(fmt.Sprintf("file-change-%d", i), i+1))
	}

	mux.SetReconciler(&multiplexer.Reconciler{Log: log.With("comp", "reconciler")})

	mux.Run(ctx)
}
