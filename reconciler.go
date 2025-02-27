package main

import (
	"context"
	"log/slog"
)

type Reconciler struct {
	log *slog.Logger
}

func (r *Reconciler) Reconcile(ctx context.Context, event string, state *State) (Result, error) {
	r.log.Info("reconciling event", slog.String("event", event), slog.Any("state", state))

	if state.fileState == 5 {
		return Result{RequeueAfter: 1}, nil
	}

	return Result{}, nil
}
