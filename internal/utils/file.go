package utils

import (
	"context"
	"log/slog"
	"os"
	"path"
	"slices"

	"github.com/fsnotify/fsnotify"
)

func FileWatcher[T any](ctx context.Context, filename string, ops []fsnotify.Op, f func(ev fsnotify.Event) T) (<-chan T, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := watcher.Add(path.Dir(filename)); err != nil {
		return nil, err
	}

	started := make(chan struct{})
	out := make(chan T)
	go func() {
		defer close(out)

		started <- struct{}{}

		if _, err := os.Stat(filename); err == nil {
			log.Info("sending initial file event")
			out <- f(fsnotify.Event{Name: filename, Op: fsnotify.Create})
			log.Info("initial file event sent")
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, open := <-watcher.Events:
				log.Info("filewatcher event", slog.Any("ev", ev))
				if !open {
					return
				}

				if ev.Name != filename || !slices.ContainsFunc(ops, func(op fsnotify.Op) bool { return ev.Has(op) }) {
					continue
				}

				out <- f(ev)
			}
		}
	}()
	<-started
	return out, nil
}
