package multiplexer

import (
	"context"
	"log/slog"
	"mux/internal/state"
	"time"
)

type Reconciler struct {
	Log *slog.Logger
}

func (r *Reconciler) Reconcile(ctx context.Context, event string, state *state.State) (Result, error) {
	r.Log.Info("reconciling event", slog.String("event", event), slog.Any("state", state))

	if state.FileState == 5 {
		return Result{RequeueAfter: 5 * time.Second}, nil
	}

	return Result{}, nil
}
