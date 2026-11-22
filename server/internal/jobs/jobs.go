// Package jobs runs in-process periodic cleanup.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/photo"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

// Retention rules.
const (
	UnconfirmedRetention = 7 * 24 * time.Hour // recognitions without a meal entry
	Interval             = 10 * time.Minute
)

// Runner deletes queued photos, old unconfirmed recognitions, and expired
// sessions.
type Runner struct {
	Store  *store.Store
	Photos photo.Storage
	Log    *slog.Logger
	Now    func() time.Time
}

// Run runs once at start and then every Interval until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		r.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once runs every cleanup one time.
func (r *Runner) Once(ctx context.Context) {
	now := r.Now()
	if n, err := r.Store.DeleteUnconfirmedRecognitions(ctx, now.Add(-UnconfirmedRetention)); err != nil {
		r.Log.Error("cleanup recognitions", "err", err)
	} else if n > 0 {
		r.Log.Info("cleanup recognitions", "deleted", n)
	}
	if n, err := r.Store.DeleteExpiredSessions(ctx, now); err != nil {
		r.Log.Error("cleanup sessions", "err", err)
	} else if n > 0 {
		r.Log.Info("cleanup sessions", "deleted", n)
	}
	keys, err := r.Store.PendingPhotoDeletions(ctx, 200)
	if err != nil {
		r.Log.Error("photo deletions", "err", err)
		return
	}
	for _, k := range keys {
		derr := r.Photos.Delete(ctx, k)
		if derr != nil {
			r.Log.Warn("photo delete", "err", derr)
		}
		if err := r.Store.FinishPhotoDeletion(ctx, k, derr); err != nil {
			r.Log.Error("photo deletions", "err", err)
		}
	}
}
