// Package jobs runs each song through its pipeline stages.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/settings"
	"karaokedock/internal/sidecar"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
)

// Pipeline stages, in order.
const (
	StageVisual   = "visual"
	StageDetails  = "details"
	StageDownload = "download"
	StageVideo    = "video"
	StageLyrics   = "lyrics"
	StageQuick    = "quick"
	StageOCR      = "ocr"
	StageSeparate = "separate"
	StagePackage  = "package"
	StageAlign    = "align"
)

// Song states.
const (
	StateQueued       = "queued"
	StateDownloading  = "downloading"
	StatePlayable     = "playable"
	StateSeparating   = "separating"
	StateKaraokeReady = "karaoke_ready"
	StateAligning     = "aligning"
	StateComplete     = "complete"
	StateFailed       = "failed"
)

var (
	fastStages = []string{StageDownload, StageVideo, StageLyrics, StageQuick, StagePackage, StageVisual}
	aiStages   = []string{StageOCR, StageSeparate, StageAlign}
)

// Notifier receives progress for the live screens.
type Notifier interface {
	SongChanged(songID string)
	SongProgress(songID, stage string, percent float64)
}

// Deps are the manager's collaborators.
type Deps struct {
	DB          *store.Store
	Settings    *settings.Manager
	Media       *media.Fs
	MetadataDir string
	Sidecar     *sidecar.Client
	Encoders    *tools.Encoders
	AMLL        *lyrics.AMLL
}

// Manager schedules jobs.
type Manager struct {
	Deps
	notify   Notifier
	wake     chan struct{}
	storage  sync.RWMutex // read during jobs, write while moving media
	forceCPU sync.Map
	active   sync.Map // song ID to the activity running now
	move     atomic.Pointer[MoveStatus]
}

// New creates a manager.
func New(d Deps) *Manager { return &Manager{Deps: d, wake: make(chan struct{}, 8)} }

// SetNotifier connects the live screens.
func (m *Manager) SetNotifier(n Notifier) { m.notify = n }

// Wake prompts idle workers to look for work.
func (m *Manager) Wake() {
	for range 4 {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}

// TempDir is the song's working folder.
func (m *Manager) TempDir(id string) string {
	return filepath.Join(m.Settings.Get(settings.TempRoot), id)
}

// SongDir is the song's metadata folder.
func (m *Manager) SongDir(id string) string {
	return filepath.Join(m.MetadataDir, "songs", id)
}

// CoverPath is an album cover's file.
func (m *Manager) CoverPath(albumID string) string {
	return filepath.Join(m.MetadataDir, "covers", albumID+".jpg")
}

// PalettePath is an album's saved colours.
func (m *Manager) PalettePath(albumID string) string {
	return filepath.Join(m.MetadataDir, "covers", albumID+"-palette.json")
}

// VisualPath is a song's saved analysis for the TV background.
func (m *Manager) VisualPath(id string) string {
	return filepath.Join(m.SongDir(id), "visual.json")
}

// backfillLyricSearch makes the lyrics of older songs searchable.
func (m *Manager) backfillLyricSearch(ctx context.Context) {
	ids, err := m.DB.SongsNeedingLyricsText(ctx)
	if err != nil {
		slog.Warn("lyric search", "err", err)
		return
	}
	for _, id := range ids {
		if doc, err := m.LoadLyrics(ctx, id); err == nil && doc != nil {
			if err := m.DB.SetLyricsText(ctx, id, doc.PlainText()); err != nil {
				slog.Warn("lyric search", "song", id, "err", err)
			}
		}
	}
}

// backfillVisuals analyses songs finished before the background existed.
func (m *Manager) backfillVisuals(ctx context.Context) error {
	ids, err := m.DB.SongIDsWithAudio(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !fileExists(m.VisualPath(id)) {
			if err := m.DB.EnqueueJobOnce(ctx, id, StageVisual); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run starts the workers: one for song details, two for quick stages and one
// for AI.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.DB.ResetRunningJobs(ctx); err != nil {
		return err
	}
	m.sweepTemp(ctx)
	if err := m.backfillVisuals(ctx); err != nil {
		slog.Warn("background analysis", "err", err)
	}
	go m.backfillLyricSearch(ctx)
	go m.worker(ctx, []string{StageDetails})
	go m.worker(ctx, fastStages)
	go m.worker(ctx, fastStages)
	go m.worker(ctx, aiStages)
	return nil
}

func (m *Manager) worker(ctx context.Context, stages []string) {
	for ctx.Err() == nil {
		if m.Settings.Bool(settings.PauseImports) || !m.Media.Healthy() {
			m.sleep(ctx)
			continue
		}
		m.storage.RLock()
		job, err := m.DB.ClaimJob(ctx, stages)
		if err != nil || job == nil {
			m.storage.RUnlock()
			if err != nil && ctx.Err() == nil {
				slog.Error("claim job", "err", err)
			}
			m.sleep(ctx)
			continue
		}
		m.execute(ctx, job)
		m.storage.RUnlock()
	}
}

func (m *Manager) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-m.wake:
	case <-time.After(3 * time.Second):
	}
}

func stageTimeout(stage string) time.Duration {
	switch stage {
	case StageOCR, StageSeparate, StageAlign:
		return 2 * time.Hour
	case StageDownload:
		return 30 * time.Minute
	default:
		return 20 * time.Minute
	}
}

func (m *Manager) execute(ctx context.Context, j *store.Job) {
	jobCtx, cancel := context.WithTimeout(ctx, stageTimeout(j.Stage))
	defer cancel()
	log := slog.With("song", j.SongID, "stage", j.Stage, "attempt", j.Attempts)
	log.Info("stage start")
	start := time.Now()
	if j.Stage != StageDetails && j.Stage != StageVisual { // these run alongside the song's other stages
		m.active.Store(j.SongID, activity{stage: j.Stage})
		defer m.active.Delete(j.SongID)
	}
	err := m.runStage(jobCtx, j)
	if ctx.Err() != nil { // shutting down: leave it to resume next start
		return
	}
	if err == nil {
		log.Info("stage done", "took", time.Since(start).Round(time.Millisecond))
		if e := m.DB.FinishJob(ctx, j.SongID, j.Stage, "done", ""); e != nil {
			log.Error("finish job", "err", e)
		}
		m.afterStage(ctx, j)
		m.changed(j.SongID)
		m.Wake()
		return
	}
	log.Warn("stage failed", "err", err)
	retry := j.Attempts < 2
	if sidecar.IsGPUOOM(err) {
		if j.Attempts == 2 {
			m.forceCPU.Store(j.SongID, true)
			retry = true
		}
	}
	if retry {
		_ = m.DB.FinishJob(ctx, j.SongID, j.Stage, "pending", err.Error())
		m.Wake()
		return
	}
	_ = m.DB.FinishJob(ctx, j.SongID, j.Stage, "failed", err.Error())
	m.handleFailure(ctx, j, err)
	m.changed(j.SongID)
}

func (m *Manager) changed(id string) {
	if m.notify != nil {
		m.notify.SongChanged(id)
	}
}

func (m *Manager) progress(id, stage string, p float64) {
	m.active.Store(id, activity{stage: stage, percent: p})
	if m.notify != nil {
		m.notify.SongProgress(id, stage, p)
	}
}

func (m *Manager) setState(ctx context.Context, id, state string) error {
	empty := ""
	err := m.DB.UpdateSong(ctx, id, store.SongUpdate{State: &state, Error: &empty})
	m.changed(id)
	return err
}

// handleFailure keeps a song usable whenever it can already play.
func (m *Manager) handleFailure(ctx context.Context, j *store.Job, cause error) {
	song, err := m.DB.GetSong(ctx, j.SongID)
	if err != nil {
		return
	}
	switch j.Stage {
	case StageDetails: // the song can still be processed
		m.startLyricsIfReady(ctx, j.SongID)
	case StageDownload, StageVideo, StageQuick:
		state, msg := StateFailed, "Couldn't get this song. Try another one."
		if strings.Contains(cause.Error(), "not a bot") {
			msg = "YouTube blocked this download. Try again later."
		}
		_ = m.DB.UpdateSong(ctx, j.SongID, store.SongUpdate{State: &state, Error: &msg})
	case StageLyrics: // lyrics are optional, so continue without them
		_ = m.DB.EnqueueJob(ctx, j.SongID, StageQuick)
		m.Wake()
	case StageOCR: // keep the timing we have
		_ = m.DB.EnqueueJob(ctx, j.SongID, StageSeparate)
		m.Wake()
	case StageSeparate, StagePackage:
		state, msg := StatePlayable, "Couldn't remove vocals. Quick mode still works."
		_ = m.DB.UpdateSong(ctx, j.SongID, store.SongUpdate{State: &state, Error: &msg})
	case StageAlign: // keep line timing
		if song.AudioKey != "" {
			_ = m.setState(ctx, j.SongID, StateComplete)
		}
		m.cleanupWavs(j.SongID)
		m.finishTemp(ctx, j.SongID)
	}
}

// afterStage queues whatever comes next.
func (m *Manager) afterStage(ctx context.Context, j *store.Job) {
	next := ""
	switch j.Stage {
	case StageDownload:
		next = StageVideo
	case StageVideo, StageDetails:
		m.startLyricsIfReady(ctx, j.SongID)
	case StageLyrics:
		next = StageQuick
	case StageQuick:
		next = StageOCR
	case StageOCR:
		next = StageSeparate
	case StageSeparate:
		next = StagePackage
	case StagePackage:
		next = StageAlign
		if err := m.DB.EnqueueJob(ctx, j.SongID, StageVisual); err != nil {
			slog.Error("enqueue", "err", err)
		}
	}
	if next != "" {
		if err := m.DB.EnqueueJob(ctx, j.SongID, next); err != nil {
			slog.Error("enqueue", "err", err)
		}
	}
}

// startLyricsIfReady begins the lyrics stage once it has both things it needs:
// the prepared media, and the song's details (or their failure).
func (m *Manager) startLyricsIfReady(ctx context.Context, id string) {
	video, err := m.DB.JobStatus(ctx, id, StageVideo)
	if err != nil || video != "done" {
		return
	}
	details, err := m.DB.JobStatus(ctx, id, StageDetails)
	if err != nil || details == "pending" || details == "running" {
		return
	}
	if err := m.DB.EnqueueJobOnce(ctx, id, StageLyrics); err != nil {
		slog.Error("enqueue", "err", err)
		return
	}
	m.Wake()
}

// Import starts the pipeline for a new song. Its details are looked up at the
// same time as the download, so they show up first.
func (m *Manager) Import(ctx context.Context, id string) error {
	if err := m.DB.EnqueueJob(ctx, id, StageDetails); err != nil {
		return err
	}
	if err := m.DB.EnqueueJob(ctx, id, StageDownload); err != nil {
		return err
	}
	m.Wake()
	return nil
}

// ErrNeedsFile means an imported song has no source left to process again.
var ErrNeedsFile = errors.New("add the file again to reprocess it")

// FilePrefix marks songs imported from a file rather than YouTube.
const FilePrefix = "file-"

// ImportFile starts the pipeline for an uploaded file already in the song's
// working folder as source.mp4 with source.info.json.
func (m *Manager) ImportFile(ctx context.Context, id string) error {
	if err := m.setState(ctx, id, StateDownloading); err != nil {
		return err
	}
	if err := m.DB.EnqueueJob(ctx, id, StageDetails); err != nil {
		return err
	}
	if err := m.DB.EnqueueJob(ctx, id, StageVideo); err != nil {
		return err
	}
	m.Wake()
	return nil
}

// Reprocess runs a song through the pipeline again.
func (m *Manager) Reprocess(ctx context.Context, id string) error {
	if strings.HasPrefix(id, FilePrefix) {
		return ErrNeedsFile
	}
	if err := m.DB.ClearJobs(ctx, id); err != nil {
		return err
	}
	if err := m.setState(ctx, id, StateQueued); err != nil {
		return err
	}
	return m.Import(ctx, id)
}

// CheckVideo makes sure the song's video is available at /media/{id}/video, by
// fetching a temporary copy when it isn't.
func (m *Manager) CheckVideo(ctx context.Context, id string) error {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	dir := m.TempDir(id)
	video := filepath.Join(dir, "video.mp4")
	if song.VideoKey != "" || fileExists(video) {
		return nil
	}
	if strings.HasPrefix(id, FilePrefix) {
		return ErrNeedsFile
	}
	if _, err := tools.Download(ctx, id, dir, m.videoHeight(), func(float64) {}); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "source.mp4"), video)
}

// EndCheck removes a video fetched for checking, once processing is finished.
func (m *Manager) EndCheck(ctx context.Context, id string) {
	if song, err := m.DB.GetSong(ctx, id); err == nil && song.State == StateComplete {
		m.finishTemp(ctx, id)
	}
}

func (m *Manager) cleanupWavs(id string) {
	dir := m.TempDir(id)
	for _, f := range []string{"audio.wav", "vocals.wav", "instrumental.wav"} {
		_ = os.Remove(filepath.Join(dir, f))
	}
}

// finishTemp removes the working folder unless the song is on screen.
func (m *Manager) finishTemp(ctx context.Context, id string) {
	if active, _ := m.DB.IsSongActive(ctx, id); active {
		return
	}
	_ = os.RemoveAll(m.TempDir(id))
}

// SongEnded deletes temporary video that was kept while the song was on screen.
func (m *Manager) SongEnded(ctx context.Context, id string) {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return
	}
	switch song.State {
	case StateComplete:
		_ = os.RemoveAll(m.TempDir(id))
	case StateKaraokeReady, StateAligning:
		dir := m.TempDir(id)
		_ = os.Remove(filepath.Join(dir, "video.mp4"))
		_ = os.Remove(filepath.Join(dir, "quick.m4a"))
	}
}

// sweepTemp clears leftovers from songs that finished while files were in use.
func (m *Manager) sweepTemp(ctx context.Context) {
	root := m.Settings.Get(settings.TempRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		song, err := m.DB.GetSong(ctx, e.Name())
		if err != nil || song.State == StateComplete {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// ErrMoveRunning means a storage move is already in progress.
var ErrMoveRunning = errors.New("a move is already running")
