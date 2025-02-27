package main

import (
	"flag"
	"log/slog"
	"os"
)

type State struct {
	fileState int
}

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

	if conn {
		GenerateConn(log, selfIPPort, remoteIPPort)
	} else {
		GenerateNoConn(log, selfIPPort, remoteIPPort)
	}

	// ctx, cancel := context.WithCancel(context.Background())

	// go func() {
	// 	time.Sleep(time.Second * 30)
	// 	cancel()
	// }()

	// mux := NewMux(log.With("comp", "mux"), func() *State {
	// 	return &State{}
	// })

	// count := 1
	// for i := 0; i < count; i++ {
	// 	mux.AddEventSource(NewFileEventSource(fmt.Sprintf("file-change-%d", i), i+1))
	// }

	// mux.SetReconciler(&Reconciler{log: log.With("comp", "reconciler")})

	// mux.Run(ctx)
}
