package jobs

import (
	"context"
	"path/filepath"
	"testing"

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
