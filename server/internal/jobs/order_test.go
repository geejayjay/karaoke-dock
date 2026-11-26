package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"karaokedock/internal/store"
)

func TestLyricsWaitForDetailsAndVideo(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := New(Deps{DB: db})
	const id = "aaaaaaaaaaa"
	lyricsQueued := func() bool {
		s, err := db.JobStatus(ctx, id, StageLyrics)
		if err != nil {
			t.Fatal(err)
		}
		return s == "pending"
	}
	for _, stage := range []string{StageDetails, StageVideo} {
		if err := db.EnqueueJob(ctx, id, stage); err != nil {
			t.Fatal(err)
		}
	}

	m.startLyricsIfReady(ctx, id)
	if lyricsQueued() {
		t.Fatal("started with nothing finished")
	}
	_ = db.FinishJob(ctx, id, StageVideo, "done", "")
	m.startLyricsIfReady(ctx, id)
	if lyricsQueued() {
		t.Fatal("started before the details were ready")
	}
	_ = db.FinishJob(ctx, id, StageDetails, "failed", "no network")
	m.startLyricsIfReady(ctx, id)
	if !lyricsQueued() {
		t.Fatal("failed details must not hold up the song")
	}
}

type progressCounter struct {
	calls int
	last  float64
}

func (p *progressCounter) SongChanged(string) {}
func (p *progressCounter) SongProgress(_ string, _ string, pct float64) {
	p.calls++
	p.last = pct
}

func TestProgressThrottlesBroadcastsWhileKeepingLivePercent(t *testing.T) {
	m := New(Deps{})
	n := &progressCounter{}
	m.SetNotifier(n)
	const id = "aaaaaaaaaaa"
	m.active.Store(id, activity{stage: StageDownload})

	// Rapid updates within 500 ms update active state immediately without flooding WebSockets.
	for i := 1; i <= 50; i++ {
		m.progress(id, StageDownload, float64(i))
	}
	v, ok := m.active.Load(id)
	if !ok || v.(activity).percent != 50 {
		t.Fatalf("active percent not updated immediately: %+v", v)
	}
	if n.calls != 1 || n.last != 1 {
		t.Fatalf("expected 1 throttled broadcast at 1%%, got %d (last=%v)", n.calls, n.last)
	}
	// Completion always broadcasts immediately.
	m.progress(id, StageDownload, 100)
	if n.calls != 2 || n.last != 100 {
		t.Fatalf("expected completion broadcast at 100%%, got %d (last=%v)", n.calls, n.last)
	}
}

func TestCancelSongStopsTrackedJobs(t *testing.T) {
	m := New(Deps{})
	const id = "aaaaaaaaaaa"
	jobCtx, cancelCtx, finish := m.trackJob(id, context.Background(), time.Minute)
	done := make(chan struct{})
	go func() {
		<-jobCtx.Done()
		finish()
		close(done)
	}()

	m.CancelSong(context.Background(), id)
	<-done
	if context.Cause(cancelCtx) != errSongCanceled {
		t.Fatalf("expected errSongCanceled cause, got %v", context.Cause(cancelCtx))
	}
	m.runMu.Lock()
	remaining := len(m.running)
	m.runMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected running map to be empty, got %d", remaining)
	}
}
