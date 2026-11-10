package jobs

import "karaokedock/internal/store"

// Progress values for a song's audio and lyrics.
const (
	PartWaiting = "waiting"
	PartWorking = "working"
	PartDone    = "done"
	PartNone    = "none" // finished, but no lyrics were found
	PartFailed  = "failed"
)

// activity is the stage a worker is running for a song right now.
type activity struct {
	stage   string
	percent float64
}

// SongStatus says how far a song has got. Overall is one of the store filters.
type SongStatus struct {
	Audio   string   `json:"audio"`
	Lyrics  string   `json:"lyrics"`
	Overall string   `json:"overall"`
	Step    string   `json:"step,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
	Message string   `json:"message,omitempty"`
	// How many lines were timed exactly from the video, out of how many.
	TimedLines int64 `json:"timedLines,omitempty"`
	LyricLines int64 `json:"lyricLines,omitempty"`
}

var stepNames = map[string]string{
	StageDownload: "Getting song",
	StageVideo:    "Preparing video",
	StageLyrics:   "Finding lyrics",
	StageQuick:    "Making quick version",
	StageOCR:      "Reading lyrics on screen",
	StageSeparate: "Removing vocals",
	StagePackage:  "Saving audio",
	StageAlign:    "Timing lyrics",
}

// Status reports a song's progress.
func (m *Manager) Status(s *store.Song) SongStatus {
	var now *activity
	if v, ok := m.active.Load(s.ID); ok {
		a := v.(activity)
		now = &a
	}
	return describe(s, now)
}

func lyricsStage(stage string) bool {
	return stage == StageLyrics || stage == StageOCR || stage == StageAlign
}

func describe(s *store.Song, now *activity) SongStatus {
	stage := ""
	if now != nil {
		stage = now.stage
	}
	// Vocals failed, or a finished song has no audio: nothing more will run.
	audioFailed := s.State == StateFailed || (s.State == StatePlayable && s.Error != "") ||
		(s.State == StateComplete && s.AudioKey == "")
	finished := s.State == StateComplete || audioFailed

	st := SongStatus{Audio: PartWaiting, Lyrics: PartWaiting, Message: s.Error, TimedLines: s.TimedLines, LyricLines: s.LyricLines}
	switch {
	case audioFailed:
		st.Audio = PartFailed
		if st.Message == "" {
			st.Message = "No audio saved. Reprocess this song."
		}
	case s.AudioKey != "" && (s.State == StateKaraokeReady || s.State == StateAligning || s.State == StateComplete):
		st.Audio = PartDone
	case s.State == StateSeparating || (stage != "" && !lyricsStage(stage)):
		st.Audio = PartWorking
	}

	switch {
	case s.State == StateFailed:
		st.Lyrics = PartFailed
	case lyricsStage(stage):
		st.Lyrics = PartWorking
	case finished && s.HasLyrics:
		st.Lyrics = PartDone
	case finished:
		st.Lyrics = PartNone
		if st.Message == "" {
			st.Message = "No lyrics found."
		}
	}

	switch {
	case st.Audio == PartFailed || st.Lyrics == PartFailed || st.Lyrics == PartNone:
		st.Overall = store.FilterProblem
	case st.Audio == PartDone && st.Lyrics == PartDone:
		st.Overall = store.FilterDone
	default:
		st.Overall = store.FilterWorking
		st.Step = "Waiting"
		if now == nil && s.State == StateQueued && s.Error != "" {
			st.Step = s.Error // waiting to try a blocked download again
		}
		if now != nil {
			st.Step = stepNames[stage]
			if now.percent > 0 {
				p := now.percent
				st.Percent = &p
			}
		}
	}
	return st
}
