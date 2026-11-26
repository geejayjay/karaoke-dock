package player

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"karaokedock/internal/settings"
)

// A queue left alone for a while belongs to a party that's over, so it's
// cleared rather than greeting the next party with old songs.

// activeKey keeps the last party activity across restarts.
const activeKey = "QUEUE_ACTIVE_AT"

const (
	activeSaveEvery = time.Minute     // how often activity is written down
	expireCheck     = 5 * time.Minute // how often an idle queue is looked for
)

// touch notes party activity: a song playing, or the queue or player changing.
func (p *Player) touch(ctx context.Context) {
	now := time.Now()
	p.activeMu.Lock()
	due := now.Sub(p.activeSaved) >= activeSaveEvery
	if due {
		p.activeSaved = now
	}
	p.activeMu.Unlock()
	if due {
		if err := p.cfg.SetRaw(ctx, map[string]string{activeKey: strconv.FormatInt(now.Unix(), 10)}); err != nil {
			slog.Warn("save queue activity", "err", err)
		}
	}
}

// lastActive is the latest of the saved activity and the newest queued song.
func (p *Player) lastActive(ctx context.Context) time.Time {
	var at time.Time
	if sec, err := strconv.ParseInt(p.cfg.Get(activeKey), 10, 64); err == nil {
		at = time.Unix(sec, 0)
	}
	if added, err := p.db.LastQueueAdd(ctx); err == nil && added.After(at) {
		at = added
	}
	return at
}

// expireIdle clears the queue when nothing has happened for the set hours. It
// reports whether it did.
func (p *Player) expireIdle(ctx context.Context, now time.Time) bool {
	hours := p.cfg.Int(settings.QueueExpireHours)
	if hours <= 0 {
		return false
	}
	last := p.lastActive(ctx)
	if last.IsZero() || now.Sub(last) < time.Duration(hours)*time.Hour {
		return false
	}
	n, err := p.db.ExpireQueue(ctx)
	if err != nil {
		slog.Error("clear idle queue", "err", err)
		return false
	}
	if n == 0 {
		return false
	}
	slog.Info("cleared an idle queue", "songs", n, "idle", now.Sub(last).Round(time.Minute))
	p.mu.Lock()
	p.state = State{SeekSeq: p.state.SeekSeq}
	st := p.state
	p.mu.Unlock()
	p.Broadcast("player.state", st)
	// "move" makes phones load the queue again.
	p.Broadcast("queue.updated", map[string]any{"op": "move", "upNext": p.upNext(ctx)})
	return true
}

func (p *Player) watchIdle(ctx context.Context) {
	t := time.NewTicker(expireCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			p.expireIdle(ctx, now)
		}
	}
}
