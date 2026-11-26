package jobs

import (
	"testing"

	"karaokedock/internal/store"
)

func TestDescribe(t *testing.T) {
	cases := []struct {
		name                   string
		song                   store.Song
		now                    *activity
		audio, lyrics, overall string
	}{
		{"new song", store.Song{State: StateQueued}, nil, PartWaiting, PartWaiting, store.FilterWorking},
		{"downloading", store.Song{State: StateDownloading}, &activity{stage: StageDownload}, PartWorking, PartWaiting, store.FilterWorking},
		{"finding lyrics", store.Song{State: StateDownloading}, &activity{stage: StageLyrics}, PartWaiting, PartWorking, store.FilterWorking},
		{"removing vocals", store.Song{State: StateSeparating, HasLyrics: true}, &activity{stage: StageSeparate, percent: 40}, PartWorking, PartWaiting, store.FilterWorking},
		{"audio done, lyrics timing", store.Song{State: StateAligning, AudioKey: "a", HasLyrics: true}, &activity{stage: StageAlign}, PartDone, PartWorking, store.FilterWorking},
		{"audio and lyrics done", store.Song{State: StateComplete, AudioKey: "a", HasLyrics: true}, nil, PartDone, PartDone, store.FilterDone},
		{"no lyrics found", store.Song{State: StateComplete, AudioKey: "a"}, nil, PartDone, PartNone, store.FilterProblem},
		{"karaoke source finished", store.Song{State: StateComplete, KaraokeVersion: true, AudioKey: "a", HasLyrics: true}, nil, PartDone, PartDone, store.FilterDone},
		{"old karaoke song, video only", store.Song{State: StateComplete, KaraokeVersion: true, VideoKey: "v"}, nil, PartFailed, PartNone, store.FilterProblem},
		{"import failed", store.Song{State: StateFailed, Error: "x"}, nil, PartFailed, PartFailed, store.FilterProblem},
		{"vocals failed", store.Song{State: StatePlayable, Error: "x", HasLyrics: true}, nil, PartFailed, PartDone, store.FilterProblem},
		{"reprocessing", store.Song{State: StateQueued, AudioKey: "old", HasLyrics: true}, nil, PartWaiting, PartWaiting, store.FilterWorking},
	}
	for _, c := range cases {
		got := describe(&c.song, c.now)
		if got.Audio != c.audio || got.Lyrics != c.lyrics || got.Overall != c.overall {
			t.Errorf("%s: got %s/%s/%s, want %s/%s/%s", c.name, got.Audio, got.Lyrics, got.Overall, c.audio, c.lyrics, c.overall)
		}
	}
}
