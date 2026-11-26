package player

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"karaokedock/internal/settings"
	"karaokedock/internal/store"
)

func idleFixture(t *testing.T) (*Player, *store.Store) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg, err := settings.Load(ctx, db, map[string]string{settings.MediaRoot: dir, settings.TempRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb"} {
		if _, err := db.InsertSong(ctx, id, "Song "+id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.AddToQueue(ctx, id, "Sam", "client-1"); err != nil {
			t.Fatal(err)
		}
	}
	return &Player{db: db, cfg: cfg, clients: map[*client]struct{}{}}, db
}

func queued(t *testing.T, db *store.Store) int {
	t.Helper()
	page, err := db.ListQueue(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

func TestIdleQueueIsCleared(t *testing.T) {
	ctx := context.Background()
	p, db := idleFixture(t)
	now := time.Now()

	if p.expireIdle(ctx, now.Add(time.Hour)) || queued(t, db) != 2 {
		t.Fatal("a queue an hour old must stay")
	}
	if !p.expireIdle(ctx, now.Add(5*time.Hour)) || queued(t, db) != 0 {
		t.Fatal("a queue idle for 5 hours must be cleared")
	}
}

func TestRecentActivityKeepsQueue(t *testing.T) {
	ctx := context.Background()
	p, db := idleFixture(t)
	later := time.Now().Add(3 * time.Hour)
	// Songs were added 5 hours before the check, but something happened 2 hours ago.
	if err := p.cfg.SetRaw(ctx, map[string]string{activeKey: strconv.FormatInt(later.Unix(), 10)}); err != nil {
		t.Fatal(err)
	}
	if p.expireIdle(ctx, later.Add(2*time.Hour)) || queued(t, db) != 2 {
		t.Fatal("recent activity must keep the queue")
	}
}

func TestZeroHoursKeepsQueue(t *testing.T) {
	ctx := context.Background()
	p, db := idleFixture(t)
	if err := p.cfg.SetRaw(ctx, map[string]string{settings.QueueExpireHours: "0"}); err != nil {
		t.Fatal(err)
	}
	if p.expireIdle(ctx, time.Now().Add(100*time.Hour)) || queued(t, db) != 2 {
		t.Fatal("0 hours must keep the queue")
	}
}
