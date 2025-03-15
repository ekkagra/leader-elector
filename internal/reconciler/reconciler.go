package reconciler

import (
	"context"
	"log/slog"
	m "mux/internal/multiplexer"
	"mux/internal/state"
	"time"
)

type Reconciler struct {
	log *slog.Logger
}

func NewReconciler(log *slog.Logger) *Reconciler {
	return &Reconciler{log: log}
}

func (r *Reconciler) Reconcile(ctx context.Context, event string, state *state.State) (m.Result, error) {
	r.log.Info("reconciling event", slog.String("event", event), slog.Any("state", state))

	if state.FileState == 5 {
		return m.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return m.Result{}, nil
}
