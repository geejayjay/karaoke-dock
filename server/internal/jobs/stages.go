package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/settings"
	"karaokedock/internal/sidecar"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
	"karaokedock/internal/visual"
)

func (m *Manager) runStage(ctx context.Context, j *store.Job) error {
	switch j.Stage {
	case StageVisual:
		return m.stageVisual(ctx, j.SongID)
	case StageDetails:
		return m.stageDetails(ctx, j.SongID)
	case StageDownload:
		return m.stageDownload(ctx, j.SongID)
	case StageVideo:
		return m.stageVideo(ctx, j.SongID)
	case StageLyrics:
		return m.stageLyrics(ctx, j.SongID)
	case StageQuick:
		return m.stageQuick(ctx, j.SongID)
	case StageOCR:
		return m.stageOCR(ctx, j.SongID)
	case StageSeparate:
		return m.stageSeparate(ctx, j.SongID)
	case StagePackage:
		return m.stagePackage(ctx, j.SongID)
	case StageAlign:
		return m.stageAlign(ctx, j.SongID)
	}
	return fmt.Errorf("unknown stage %q", j.Stage)
}

func (m *Manager) videoHeight() int {
	switch m.Settings.Get(settings.VideoKeep) {
	case "720p":
		return 720
	case "480p":
		return 480
	}
	return 360 // only temporary, so the smallest that OCR can still read
}

func ptr[T any](v T) *T { return &v }

func (m *Manager) stageDownload(ctx context.Context, id string) error {
	if err := m.setState(ctx, id, StateDownloading); err != nil {
		return err
	}
	dir := m.TempDir(id)
	info, err := tools.Download(ctx, id, dir, m.videoHeight(), func(p float64) { m.progress(id, StageDownload, p) })
	if err != nil {
		return err
	}
	return m.DB.UpdateSong(ctx, id, store.SongUpdate{DurationMs: ptr(int64(info.Duration * 1000))})
}

// useCover points the song at its album's art. Pictures belong to the album, so
// a song stores none of its own, and one with no album art has no picture.
func (m *Manager) useCover(ctx context.Context, id, albumID string) error {
	u := store.SongUpdate{HasThumb: ptr(false), HasBg: ptr(false), AccentColor: ptr("")}
	// Left by earlier versions, which kept copies for each song and album.
	_ = os.Remove(filepath.Join(m.SongDir(id), "thumb.jpg"))
	_ = os.Remove(filepath.Join(m.SongDir(id), "bg.jpg"))
	if albumID != "" {
		_ = os.Remove(filepath.Join(m.MetadataDir, "covers", albumID+"-bg.jpg"))
	}
	cover := ""
	if albumID != "" {
		cover = m.CoverPath(albumID)
	}
	if cover == "" || !fileExists(cover) {
		return m.DB.UpdateSong(ctx, id, u)
	}
	u.HasThumb = ptr(true)
	if c, err := tools.AccentColor(cover); err == nil {
		u.AccentColor = &c
	}
	m.ensurePalette(albumID)
	return m.DB.UpdateSong(ctx, id, u)
}

// ensurePalette saves an album's colours once.
func (m *Manager) ensurePalette(albumID string) {
	if albumID == "" || fileExists(m.PalettePath(albumID)) || !fileExists(m.CoverPath(albumID)) {
		return
	}
	raw, err := visual.PaletteJSON(m.CoverPath(albumID))
	if err != nil {
		slog.Warn("album colours", "album", albumID, "err", err)
		return
	}
	if _, err := media.AtomicCopy(m.PalettePath(albumID), bytes.NewReader(raw)); err != nil {
		slog.Warn("album colours", "album", albumID, "err", err)
	}
}

// SyncSongCover updates a song's thumbnail flag and accent color from its album.
func (m *Manager) SyncSongCover(ctx context.Context, songID string) error {
	song, err := m.DB.GetSong(ctx, songID)
	if err != nil {
		return err
	}
	if err := m.useCover(ctx, songID, song.AlbumID); err != nil {
		return err
	}
	m.changed(songID)
	return nil
}

// RefreshAlbumCover recomputes an album's palette and accent color and updates its songs.
func (m *Manager) RefreshAlbumCover(ctx context.Context, albumID string) error {
	if albumID == "" {
		return nil
	}
	_ = os.Remove(m.PalettePath(albumID))
	cover := m.CoverPath(albumID)
	coverKey, accent := "", ""
	if fileExists(cover) {
		coverKey = "covers/" + albumID + ".jpg"
		if c, err := tools.AccentColor(cover); err == nil {
			accent = c
		}
		m.ensurePalette(albumID)
	}
	songIDs, err := m.DB.SetAlbumCover(ctx, albumID, coverKey, accent)
	if err != nil {
		return err
	}
	for _, sid := range songIDs {
		m.changed(sid)
	}
	return nil
}

// ErrNoMatch means MusicBrainz had no recording matching the request.
var ErrNoMatch = errors.New("no matching song found")

// ReidentifyReq holds optional hints or a chosen recording for re-identifying a song.
type ReidentifyReq struct {
	RecordingID    string `json:"recordingId"`
	ReleaseGroupID string `json:"releaseGroupId"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	RefreshLyrics  bool   `json:"refreshLyrics"`
}

// Reidentify looks up a song's details, album and cover art again, and optionally refreshes its lyrics.
func (m *Manager) Reidentify(ctx context.Context, id string, req ReidentifyReq) (*store.Song, error) {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return nil, err
	}
	durationMs := song.DurationMs
	var details *lyrics.Details
	title := strings.TrimSpace(req.Title)
	artist := strings.TrimSpace(req.Artist)

	switch {
	case req.RecordingID != "":
		details, err = lyrics.LookupRecording(ctx, req.RecordingID, req.ReleaseGroupID)
		if err != nil {
			return nil, err
		}
	case title != "" || artist != "":
		if title == "" {
			title = song.Title
		}
		firstArtist := artist
		if i := strings.Index(firstArtist, ","); i >= 0 {
			firstArtist = strings.TrimSpace(firstArtist[:i])
		}
		details, err = lyrics.LookupDetails(ctx, firstArtist, title, durationMs)
		if err != nil {
			return nil, err
		}
		if details == nil && firstArtist == "" {
			_, _, details = m.resolveNames(ctx, id, &tools.Info{ID: id, Title: title, Duration: float64(durationMs) / 1000}, durationMs)
		}
	default:
		var info *tools.Info
		if rawInfo, err := readInfo(m.TempDir(id)); err == nil {
			info = rawInfo
		} else if raw, err := os.ReadFile(m.sourcePath(id)); err == nil {
			var src tools.Info
			if json.Unmarshal(raw, &src) == nil && src.Title != "" {
				info = &src
			}
		} else if tools.VideoID.MatchString(id) {
			if fetched, err := tools.FetchInfo(ctx, id); err == nil {
				info = fetched
			}
		}
		if info != nil {
			if durationMs == 0 && info.Duration > 0 {
				durationMs = int64(info.Duration * 1000)
			}
			artist, title, details = m.resolveNames(ctx, id, info, durationMs)
		}
		if details == nil {
			firstArtist := song.Artist
			if i := strings.Index(firstArtist, ","); i >= 0 {
				firstArtist = strings.TrimSpace(firstArtist[:i])
			}
			details, err = lyrics.LookupDetails(ctx, firstArtist, song.Title, durationMs)
			if err != nil {
				return nil, err
			}
		}
	}

	if details == nil {
		return nil, ErrNoMatch
	}

	meta := Meta{
		Title:      firstNonEmpty(details.Title, title, song.Title),
		Artists:    details.Artists,
		Album:      details.Album,
		Genre:      details.Genre,
		Language:   details.Language,
		YouTubeID:  id,
		DurationMs: durationMs,
		Matched:    true,
	}
	var artists []store.ArtistInput
	for _, a := range details.Artists {
		artists = append(artists, store.ArtistInput{MBID: a.MBID, Name: a.Name, Gender: a.Gender})
	}
	var album *store.AlbumInput
	if details.Album != "" {
		rgID := details.ReleaseGroupID
		album = &store.AlbumInput{ID: rgID, Title: details.Album, Year: details.Year}
		if rgID != "" {
			if ok, err := lyrics.DownloadCover(ctx, rgID, m.CoverPath(rgID)); err != nil {
				slog.Info("cover download failed", "song", id, "err", err)
			} else if ok || fileExists(m.CoverPath(rgID)) {
				album.CoverKey = "covers/" + rgID + ".jpg"
				_ = os.Remove(m.PalettePath(rgID))
			}
		}
	}
	if err := m.DB.SetSongDetails(ctx, id, artists, album); err != nil {
		return nil, err
	}
	u := store.SongUpdate{Title: &meta.Title, Genre: &meta.Genre}
	if meta.Language != "" {
		u.Language = ptr(lyrics.LanguageName(meta.Language))
	}
	if err := m.DB.UpdateSong(ctx, id, u); err != nil {
		return nil, err
	}
	updated, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := m.useCover(ctx, id, updated.AlbumID); err != nil {
		return nil, err
	}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	if _, err := media.AtomicCopy(filepath.Join(m.SongDir(id), "meta.json"), bytes.NewReader(metaJSON)); err != nil {
		return nil, err
	}
	if req.RefreshLyrics {
		_ = m.RefreshLyrics(ctx, id, meta)
	}
	m.changed(id)
	return m.DB.GetSong(ctx, id)
}

// RefreshLyrics fetches lyrics for a song's current metadata without wiping its audio.
func (m *Manager) RefreshLyrics(ctx context.Context, id string, meta Meta) error {
	dir := m.TempDir(id)
	info, _ := readInfo(dir)
	if info == nil {
		info = &tools.Info{ID: id, Title: meta.Title, Duration: float64(meta.DurationMs) / 1000}
	}
	doc := m.findLyrics(ctx, id, dir, info, meta)
	if doc == nil {
		return nil
	}
	doc.Language = meta.Language
	source := filepath.Join(dir, "source.mp4")
	if fileExists(source) {
		if env, err := tools.Envelope(ctx, source); err == nil {
			starts := make([]int64, 0, len(doc.Lines))
			for _, l := range doc.Lines {
				starts = append(starts, l.StartMs)
			}
			if shift, ok := lyrics.FindOffset(env, starts, 20000); ok {
				doc.Shift(shift)
			}
		}
	}
	return m.SaveLyrics(ctx, id, doc)
}

// stageVisual listens to the finished audio once and saves what the background
// needs, so the TV has nothing to work out while it plays.
func (m *Manager) stageVisual(ctx context.Context, id string) error {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	if song.AudioKey == "" {
		return nil
	}
	path, err := m.Media.Path(song.AudioKey)
	if err != nil {
		return err
	}
	samples, err := tools.MonoSamples(ctx, path, visual.SampleRate)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(visual.Analyze(samples))
	if err != nil {
		return err
	}
	if _, err := media.AtomicCopy(m.VisualPath(id), bytes.NewReader(raw)); err != nil {
		return err
	}
	m.ensurePalette(song.AlbumID)
	return nil
}

// hasSavedDetails reports whether a song already has our own details, which
// must not be replaced by what YouTube says when it's processed again.
func hasSavedDetails(s *store.Song) bool { return s.Artist != "" }

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (m *Manager) stageVideo(ctx context.Context, id string) error {
	dir := m.TempDir(id)
	src, dst := filepath.Join(dir, "source.mp4"), filepath.Join(dir, "video.mp4")
	return m.Encoders.PrepVideo(ctx, m.Settings.Get(settings.VideoEncoder), src, dst, m.videoHeight())
}

func (m *Manager) storeFile(path, key string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = m.Media.Write(key, f)
	return err
}

func readInfo(dir string) (*tools.Info, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "source.info.json"))
	if err != nil {
		return nil, err
	}
	var info tools.Info
	return &info, json.Unmarshal(raw, &info)
}

// Meta is the stored meta.json.
type Meta struct {
	Title      string          `json:"title"`
	Artists    []lyrics.Artist `json:"artists"`
	Album      string          `json:"album,omitempty"`
	Genre      string          `json:"genre,omitempty"`
	Language   string          `json:"language,omitempty"`
	YouTubeID  string          `json:"youtubeId"`
	DurationMs int64           `json:"durationMs"`
	Matched    bool            `json:"matched"` // found in the song database
}

const skipAlignMarker = "skip-align"

// stageDetails finds the song's title, singers, album and art. It runs on its
// own worker from the moment a song is added, so none of this waits for the
// download.
func (m *Manager) stageDetails(ctx context.Context, id string) error {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	info, err := m.detailsSource(ctx, id, song)
	if err != nil {
		return err
	}
	durationMs := int64(info.Duration * 1000)

	saved := hasSavedDetails(song)
	var savedNames []string
	var artist, title string
	if saved {
		savedNames = strings.Split(song.Artist, ", ")
		artist, title = savedNames[0], song.Title
	}
	first := store.SongUpdate{}
	if durationMs > 0 {
		first.DurationMs = &durationMs
	}
	if lyrics.IsKaraokeTitle(info.Title) && !saved {
		first.KaraokeVersion = ptr(true)
	}
	if err := m.DB.UpdateSong(ctx, id, first); err != nil {
		return err
	}

	var details *lyrics.Details
	if saved {
		if details, err = lyrics.LookupDetails(ctx, artist, title, durationMs); err != nil {
			slog.Info("song details lookup failed", "song", id, "err", err)
			details = nil
		}
	} else {
		artist, title, details = m.resolveNames(ctx, id, info, durationMs)
	}

	meta := Meta{Title: title, YouTubeID: id, DurationMs: durationMs, Album: info.Album, Matched: details != nil}
	var artists []store.ArtistInput
	var album *store.AlbumInput
	if details != nil {
		meta.Title, meta.Genre, meta.Language = firstNonEmpty(details.Title, title), details.Genre, details.Language
		meta.Artists = details.Artists
		for _, a := range details.Artists {
			artists = append(artists, store.ArtistInput{MBID: a.MBID, Name: a.Name, Gender: a.Gender})
		}
		if details.Album != "" && (!saved || song.Album == "") {
			meta.Album = details.Album
			album = &store.AlbumInput{ID: details.ReleaseGroupID, Title: details.Album, Year: details.Year}
			if ok, err := lyrics.DownloadCover(ctx, details.ReleaseGroupID, m.CoverPath(details.ReleaseGroupID)); err != nil {
				slog.Info("cover download failed", "song", id, "err", err)
			} else if ok {
				album.CoverKey = "covers/" + details.ReleaseGroupID + ".jpg"
			}
		}
	}
	if len(artists) == 0 && artist != "" {
		artists = []store.ArtistInput{{Name: artist}}
		meta.Artists = []lyrics.Artist{{Name: artist}}
	}
	if meta.Language == "" && len(info.Language) >= 2 {
		meta.Language = strings.ToLower(info.Language[:2])
	}
	if saved {
		meta.Title, meta.Genre = song.Title, song.Genre
		if album != nil { // a blank is filled in, without changing the saved singers
			named := make([]store.ArtistInput, 0, len(savedNames))
			for _, name := range savedNames {
				a := store.ArtistInput{Name: name}
				for _, found := range details.Artists {
					if strings.EqualFold(found.Name, name) {
						a.MBID, a.Gender = found.MBID, found.Gender
					}
				}
				named = append(named, a)
			}
			if err := m.DB.SetSongDetails(ctx, id, named, album); err != nil {
				return err
			}
		} else {
			meta.Album = song.Album
		}
		if details == nil {
			meta.Artists = nil
			for _, name := range savedNames {
				meta.Artists = append(meta.Artists, lyrics.Artist{Name: name})
			}
		}
	} else {
		if err := m.DB.SetSongDetails(ctx, id, artists, album); err != nil {
			return err
		}
		u := store.SongUpdate{Title: &meta.Title, Genre: &meta.Genre}
		if meta.Language != "" {
			u.Language = ptr(lyrics.LanguageName(meta.Language))
		}
		if err := m.DB.UpdateSong(ctx, id, u); err != nil {
			return err
		}
	}
	albumID := song.AlbumID
	if album != nil {
		albumID = album.ID
	}
	if err := m.useCover(ctx, id, albumID); err != nil {
		return err
	}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	_, err = media.AtomicCopy(filepath.Join(m.SongDir(id), "meta.json"), bytes.NewReader(metaJSON))
	return err
}

// sourcePath is what YouTube's search said about a song when it was added.
func (m *Manager) sourcePath(id string) string {
	return filepath.Join(m.SongDir(id), "source.json")
}

// SaveSource keeps the search result's raw title and channel for the lookup,
// so it can start before anything is downloaded.
func (m *Manager) SaveSource(id, title, channel string, durationMs int64) {
	raw, err := json.Marshal(tools.Info{ID: id, Title: title, Channel: channel, Duration: float64(durationMs) / 1000})
	if err != nil {
		return
	}
	if _, err := media.AtomicCopy(m.sourcePath(id), bytes.NewReader(raw)); err != nil {
		slog.Warn("save source", "song", id, "err", err)
	}
}

// detailsSource finds what's known about the video. Only a pasted link has to
// ask YouTube first.
func (m *Manager) detailsSource(ctx context.Context, id string, song *store.Song) (*tools.Info, error) {
	if info, err := readInfo(m.TempDir(id)); err == nil {
		return info, nil
	}
	if raw, err := os.ReadFile(m.sourcePath(id)); err == nil {
		var info tools.Info
		if json.Unmarshal(raw, &info) == nil && info.Title != "" {
			return &info, nil
		}
	}
	if hasSavedDetails(song) {
		return &tools.Info{ID: id, Title: song.Title}, nil
	}
	return tools.FetchInfo(ctx, id)
}

// maxNameLookups limits how many readings go to MusicBrainz, which allows only
// one request a second.
const maxNameLookups = 3

// resolveNames works out the singer and title, using the video's title, channel
// and music details, what's already in the library, and the music databases.
func (m *Manager) resolveNames(ctx context.Context, id string, info *tools.Info, durationMs int64) (string, string, *lyrics.Details) {
	artist, title, details, found := m.tryNames(ctx, id, info, durationMs)
	// A search result has no music details. If nothing matched, get them.
	if !found && info.Track == "" && tools.VideoID.MatchString(id) {
		if full, err := tools.FetchInfo(ctx, id); err == nil && full.Track != "" {
			if a, t, d, ok := m.tryNames(ctx, id, full, durationMs); ok {
				return a, t, d
			}
		}
	}
	return artist, title, details
}

func (m *Manager) tryNames(ctx context.Context, id string, info *tools.Info, durationMs int64) (string, string, *lyrics.Details, bool) {
	known := func(name string) bool {
		yes, err := m.DB.ArtistExists(ctx, name)
		return err == nil && yes
	}
	clues := lyrics.NameClues{RawTitle: info.Title, Track: info.Track, Artist: info.Artist, Channel: firstNonEmpty(info.Channel, info.Uploader), DurationMs: durationMs}
	guesses := lyrics.Guesses(clues, known)
	if len(guesses) == 0 {
		return "", lyrics.CleanTitle(info.Title), nil, false
	}
	confirmed, ok := lyrics.ConfirmNames(ctx, guesses, durationMs)
	if ok {
		guesses = append([]lyrics.Guess{confirmed}, guesses...)
	}
	for i, g := range guesses {
		if i == maxNameLookups {
			break
		}
		d, err := lyrics.LookupDetails(ctx, g.Artist, g.Title, durationMs)
		if err != nil {
			slog.Info("song details lookup failed", "song", id, "err", err)
			break
		}
		if d != nil {
			slog.Info("identified song", "song", id, "artist", g.Artist, "title", g.Title)
			return g.Artist, g.Title, d, true
		}
	}
	return guesses[0].Artist, guesses[0].Title, nil, ok
}

func (m *Manager) readMeta(id string) (Meta, error) {
	var meta Meta
	raw, err := os.ReadFile(filepath.Join(m.SongDir(id), "meta.json"))
	if err != nil {
		return meta, err
	}
	return meta, json.Unmarshal(raw, &meta)
}

func (m *Manager) stageLyrics(ctx context.Context, id string) error {
	dir := m.TempDir(id)
	info, err := readInfo(dir)
	if err != nil {
		return err
	}
	meta, err := m.readMeta(id)
	if err != nil { // the details stage failed, and the download has what it needs
		if err := m.stageDetails(ctx, id); err != nil {
			return err
		}
		if meta, err = m.readMeta(id); err != nil {
			return err
		}
	}
	// The details may have run before the download gave us these.
	meta.DurationMs = int64(info.Duration * 1000)
	if meta.Language == "" && len(info.Language) >= 2 {
		meta.Language = strings.ToLower(info.Language[:2])
	}

	doc := m.findLyrics(ctx, id, dir, info, meta)
	if doc == nil {
		return nil
	}
	doc.Language = meta.Language
	source := filepath.Join(dir, "source.mp4")
	clear := false
	if env, err := tools.Envelope(ctx, source); err == nil {
		starts := make([]int64, 0, len(doc.Lines))
		for _, l := range doc.Lines {
			starts = append(starts, l.StartMs)
		}
		if shift, ok := lyrics.FindOffset(env, starts, 20000); ok {
			doc.Shift(shift)
			clear = true
			slog.Info("applied automatic lyric offset", "song", id, "ms", shift)
		}
	}
	if doc.Source == "amll" && clear {
		_ = os.WriteFile(filepath.Join(dir, skipAlignMarker), nil, 0o644)
	}
	return m.SaveLyrics(ctx, id, doc)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// SaveLyrics writes a document through the media store and marks the song.
func (m *Manager) SaveLyrics(ctx context.Context, id string, doc *lyrics.Doc) error {
	doc.DropDirections()
	doc.BreakLong()
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	key := media.LyricsKey(id)
	if _, err := m.Media.Write(key, bytes.NewReader(raw)); err != nil {
		return err
	}
	if err := m.DB.SetLyricsText(ctx, id, doc.PlainText()); err != nil {
		slog.Warn("lyric search", "song", id, "err", err)
	}
	return m.DB.UpdateSong(ctx, id, store.SongUpdate{
		LyricsKey: &key, HasLyrics: ptr(len(doc.Lines) > 0),
		TimedLines: ptr(int64(doc.ExactLines())), LyricLines: ptr(int64(len(doc.Lines))),
	})
}

// LoadLyrics reads a song's document, or nil when it has none.
func (m *Manager) LoadLyrics(ctx context.Context, id string) (*lyrics.Doc, error) {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return nil, err
	}
	if song.LyricsKey == "" {
		return nil, nil
	}
	f, err := m.Media.Open(song.LyricsKey)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var doc lyrics.Doc
	if err := json.NewDecoder(f).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// findLyrics tries the sources in order and applies the voice-part rules.
func (m *Manager) findLyrics(ctx context.Context, id, dir string, info *tools.Info, meta Meta) *lyrics.Doc {
	onePerformer := len(meta.Artists) == 1 && meta.Matched
	artist := ""
	if len(meta.Artists) > 0 {
		artist = meta.Artists[0].Name
	}

	if !onePerformer && m.AMLL != nil {
		lines, agents, err := m.AMLL.Find(meta.Title, artist)
		if err != nil {
			slog.Warn("amll", "song", id, "err", err)
		}
		if len(lines) > 0 {
			return amllDoc(lines, agents, meta.Artists)
		}
	}

	doc := &lyrics.Doc{Version: 1, Timing: "line", Voices: []lyrics.Voice{}}
	r, err := lyrics.FetchLRCLIB(ctx, artist, meta.Title, float64(meta.DurationMs)/1000)
	if err != nil {
		slog.Info("lrclib lookup failed", "song", id, "err", err)
	}
	if r != nil && r.SyncedLyrics != "" {
		doc.Source = "lrclib"
		doc.Lines = lyrics.ParseLRC(r.SyncedLyrics)
		doc.Original = &lyrics.Original{Plain: r.PlainLyrics, Synced: r.SyncedLyrics}
	} else if lines := m.captionLines(ctx, id, dir, info); len(lines) > 0 {
		doc.Source = "captions"
		doc.Lines = lines
	}
	if len(doc.Lines) == 0 {
		return nil
	}
	if !onePerformer && doc.ApplyMarkers() {
		labelVoices(doc, meta.Artists)
	}
	return doc
}

func amllDoc(lines []lyrics.Line, agents []string, artists []lyrics.Artist) *lyrics.Doc {
	ids := map[string]string{}
	doc := &lyrics.Doc{Version: 1, Source: "amll", Timing: "word", Voices: []lyrics.Voice{}, Lines: lines}
	names := []string{"a", "b", "c", "d"}
	for i, ag := range agents {
		if i >= len(names) {
			break
		}
		ids[ag] = names[i]
		label := fmt.Sprintf("Part %d", i+1)
		if i < len(artists) {
			label = artists[i].Name
		}
		doc.Voices = append(doc.Voices, lyrics.Voice{ID: names[i], Label: label, Source: "amll"})
	}
	if len(agents) < 2 {
		doc.Voices = []lyrics.Voice{}
	}
	for i := range doc.Lines {
		if len(agents) < 2 {
			doc.Lines[i].Voice = ""
		} else {
			doc.Lines[i].Voice = ids[doc.Lines[i].Voice]
		}
	}
	labelVoices(doc, artists)
	return doc
}

// labelVoices uses Boy and Girl only when MusicBrainz shows a male and a female
// performer, and artist names otherwise.
func labelVoices(doc *lyrics.Doc, artists []lyrics.Artist) {
	male, female := "", ""
	for _, a := range artists {
		if a.Gender == "male" && male == "" {
			male = a.Name
		}
		if a.Gender == "female" && female == "" {
			female = a.Name
		}
	}
	mixed := male != "" && female != ""
	for i := range doc.Voices {
		v := &doc.Voices[i]
		if v.ID == "both" {
			continue
		}
		if doc.Source == "amll" && mixed && i < len(artists) && i < 2 {
			switch artists[i].Gender {
			case "male":
				v.Label = "Boy"
			case "female":
				v.Label = "Girl"
			}
		}
		if doc.Source != "amll" && !mixed && len(artists) >= 2 && i < 2 {
			v.Label = artists[i].Name
		}
	}
}

var vttCue = regexp.MustCompile(`^(\d{2}:)?(\d{2}):(\d{2})\.(\d{3})\s+-->\s+(\d{2}:)?(\d{2}):(\d{2})\.(\d{3})`)
var vttTag = regexp.MustCompile(`<[^>]+>`)

// captionLines fetches manual captions, ignoring auto-generated ones.
func (m *Manager) captionLines(ctx context.Context, id, dir string, info *tools.Info) []lyrics.Line {
	lang := ""
	for k := range info.Subtitles {
		if k == "live_chat" {
			continue
		}
		if lang == "" || strings.HasPrefix(k, "en") {
			lang = k
		}
	}
	if lang == "" {
		return nil
	}
	path, err := tools.FetchCaptions(ctx, id, dir, lang)
	if err != nil || path == "" {
		if err != nil {
			slog.Info("captions failed", "song", id, "err", err)
		}
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseVTT(string(raw))
}

func vttMs(h, mm, s, ms string) int64 {
	var hh, mi, se, milli int64
	fmt.Sscanf(strings.TrimSuffix(h, ":"), "%d", &hh)
	fmt.Sscanf(mm, "%d", &mi)
	fmt.Sscanf(s, "%d", &se)
	fmt.Sscanf(ms, "%d", &milli)
	return hh*3600000 + mi*60000 + se*1000 + milli
}

func parseVTT(text string) []lyrics.Line {
	var b strings.Builder
	rows := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	last := ""
	for i := 0; i < len(rows); i++ {
		c := vttCue.FindStringSubmatch(rows[i])
		if c == nil {
			continue
		}
		start, end := vttMs(c[1], c[2], c[3], c[4]), vttMs(c[5], c[6], c[7], c[8])
		var parts []string
		for i+1 < len(rows) && strings.TrimSpace(rows[i+1]) != "" {
			i++
			parts = append(parts, strings.TrimSpace(vttTag.ReplaceAllString(rows[i], "")))
		}
		line := strings.Trim(strings.Join(parts, " "), "♪♫ ")
		if line == "" || line == last {
			continue
		}
		last = line
		fmt.Fprintf(&b, "[%02d:%02d.%03d]%s\n[%02d:%02d.%03d]\n", start/60000, start/1000%60, start%1000, line, end/60000, end/1000%60, end%1000)
	}
	return lyrics.ParseLRC(b.String())
}

func (m *Manager) stageQuick(ctx context.Context, id string) error {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	dir := m.TempDir(id)
	src, dst := filepath.Join(dir, "source.mp4"), filepath.Join(dir, "quick.m4a")
	extract := tools.QuickAudio
	if song.KaraokeVersion {
		extract = tools.PlainAudio // already has no lead vocal
	}
	if err := extract(ctx, src, dst); err != nil {
		return err
	}
	return m.setState(ctx, id, StatePlayable)
}

// stageOCR reads lyrics shown in the video, which is only here while the song
// is processing, and uses them to correct the timing. Karaoke videos with no
// published lyrics get theirs from the screen.
func (m *Manager) stageOCR(ctx context.Context, id string) error {
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	doc, err := m.LoadLyrics(ctx, id)
	if err != nil {
		return err
	}
	known := doc != nil && len(doc.Lines) > 0
	if (known && doc.Timing == "word") || (!known && !song.KaraokeVersion) {
		return nil
	}
	dir, err := filepath.Abs(m.TempDir(id))
	if err != nil {
		return err
	}
	video := filepath.Join(dir, "video.mp4")
	if !fileExists(video) {
		return nil
	}
	type ocrLine struct {
		Text      string     `json:"text"`
		StartMs   int64      `json:"startMs"`
		EndMs     int64      `json:"endMs"`
		Words     []string   `json:"words,omitempty"`
		WordTimes [][2]int64 `json:"wordTimes,omitempty"`
	}
	sent := []ocrLine{}
	if known {
		for _, l := range doc.Lines {
			words := make([]string, len(l.Words))
			for i, w := range l.Words {
				words[i] = w.Text
			}
			sent = append(sent, ocrLine{Text: l.Text(), StartMs: l.StartMs, EndMs: l.EndMs, Words: words})
		}
	}
	input, output := filepath.Join(dir, "screen-in.json"), filepath.Join(dir, "screen-out.json")
	raw, _ := json.Marshal(map[string]any{"lines": sent})
	if err := os.WriteFile(input, raw, 0o644); err != nil {
		return err
	}
	ignore := []string{song.Title, song.Artist}
	if info, err := readInfo(dir); err == nil {
		ignore = append(ignore, info.Channel, info.Uploader)
	}
	if _, err := m.Sidecar.Call(ctx, sidecar.Request{
		"op": "ocr", "input": video, "lyrics": input, "output": output, "durationMs": song.DurationMs, "buildLyrics": !known, "ignore": ignore,
	}, func(p float64) { m.progress(id, StageOCR, p) }); err != nil {
		return err
	}
	raw, err = os.ReadFile(output)
	if err != nil {
		return err
	}
	var res struct {
		Found    bool         `json:"found"`
		Matched  int          `json:"matched"`
		OffsetMs int64        `json:"offsetMs"`
		Lines    []ocrLine    `json:"lines"`
		Rows     []lyrics.Row `json:"rows"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.Found {
		return nil
	}
	if known {
		if len(res.Lines) != len(doc.Lines) {
			return errors.New("screen timing returned a different number of lines")
		}
		for i, l := range res.Lines {
			doc.Lines[i].Retime(l.StartMs, l.EndMs)
			doc.Lines[i].SetWordTimes(l.WordTimes)
		}
		if len(res.Rows) > 0 {
			// Same words and timing, arranged the way the video shows them.
			rowsTimed := doc.Reflow(res.Rows)
			slog.Info("laid lyrics out like the video", "song", id, "rows", len(res.Rows), "timedRows", rowsTimed)
		}
	} else {
		doc = &lyrics.Doc{Version: 1, Source: "ocr", Timing: "line", Voices: []lyrics.Voice{}}
		for _, l := range res.Lines {
			line := lyrics.NewLine(l.Text, l.StartMs, l.EndMs)
			line.SetWordTimes(l.WordTimes)
			doc.Lines = append(doc.Lines, line)
		}
		lyrics.FitUntimed(doc.Lines)
	}
	if doc.MostlyFromVideo() {
		doc.Timing = "word"
	}
	slog.Info("timed lyrics from the screen", "song", id, "matched", res.Matched, "lines", len(res.Lines), "offsetMs", res.OffsetMs)
	return m.SaveLyrics(ctx, id, doc)
}

func (m *Manager) device(id string) string {
	if _, forced := m.forceCPU.Load(id); forced {
		return "cpu"
	}
	return m.Settings.Get(settings.MLDevice)
}

func (m *Manager) modelDir() string { return filepath.Join(m.MetadataDir, "models") }

func (m *Manager) stageSeparate(ctx context.Context, id string) error {
	if err := m.setState(ctx, id, StateSeparating); err != nil {
		return err
	}
	dir, err := filepath.Abs(m.TempDir(id))
	if err != nil {
		return err
	}
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	if song.KaraokeVersion {
		if !m.sampleHasVocals(ctx, id, dir, song.DurationMs) {
			return tools.ToWav(ctx, filepath.Join(dir, "source.mp4"), filepath.Join(dir, "instrumental.wav"))
		}
		// Labelled karaoke but someone is singing, so treat it as a normal song.
		if err := m.DB.UpdateSong(ctx, id, store.SongUpdate{KaraokeVersion: ptr(false)}); err != nil {
			return err
		}
	}
	wav := filepath.Join(dir, "audio.wav")
	if err := tools.ToWav(ctx, filepath.Join(dir, "source.mp4"), wav); err != nil {
		return err
	}
	modelDir, err := filepath.Abs(m.modelDir())
	if err != nil {
		return err
	}
	_, err = m.Sidecar.Call(ctx, sidecar.Request{
		"op": "separate", "input": wav, "outDir": dir, "model": m.Settings.Get(settings.SeparationModel),
		"modelDir": modelDir, "device": m.device(id), "threads": m.Settings.Int(settings.MLThreads),
	}, func(p float64) { m.progress(id, StageSeparate, p) })
	return err
}

// vocalThresholdDb is how loud vocals must be against the mix to count as a lead
// vocal. Backing singers and leftover noise on karaoke tracks sit well below it.
const vocalThresholdDb = -16.0

// sampleHasVocals separates a 30-second sample. It costs about 25 seconds, and
// saves minutes when there's nothing to remove. If it can't tell, it trusts the
// title and reports none.
func (m *Manager) sampleHasVocals(ctx context.Context, id, dir string, durationMs int64) bool {
	sampleDir := filepath.Join(dir, "sample")
	defer os.RemoveAll(sampleDir)
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		return false
	}
	wav := filepath.Join(sampleDir, "clip.wav")
	if err := tools.SampleWav(ctx, filepath.Join(dir, "source.mp4"), wav, durationMs); err != nil {
		slog.Warn("vocal sample", "song", id, "err", err)
		return false
	}
	modelDir, err := filepath.Abs(m.modelDir())
	if err != nil {
		return false
	}
	ev, err := m.Sidecar.Call(ctx, sidecar.Request{
		"op": "vocal_level", "input": wav, "outDir": sampleDir, "model": m.Settings.Get(settings.SeparationModel),
		"modelDir": modelDir, "device": m.device(id), "threads": m.Settings.Int(settings.MLThreads),
	}, nil)
	if err != nil {
		slog.Warn("vocal sample", "song", id, "err", err)
		return false
	}
	slog.Info("checked for vocals", "song", id, "vocalDb", ev.VocalDb)
	return ev.VocalDb > vocalThresholdDb
}

const (
	trimPadStartMs = 200  // silence kept before the music
	trimPadEndMs   = 500  // silence kept after the music
	trimMinMs      = 1000 // not worth cutting less than this
	trimLyricGapMs = 500  // never cut this close to a lyric
)

// planTrim decides how much silence to cut from each end of the song. Lyrics
// stay timed to the uncut song, and what's saved here lines them up.
func (m *Manager) planTrim(ctx context.Context, id, path string) tools.Trim {
	total, err := tools.DurationMs(ctx, path)
	if err != nil {
		slog.Warn("silence check", "song", id, "err", err)
		return tools.Trim{}
	}
	silentStart, silentEnd, err := tools.SilentEnds(ctx, path, total)
	if err != nil {
		slog.Warn("silence check", "song", id, "err", err)
		return tools.Trim{}
	}
	start, end := silentStart-trimPadStartMs, silentEnd-trimPadEndMs
	if doc, err := m.LoadLyrics(ctx, id); err == nil && doc != nil && len(doc.Lines) > 0 {
		start = min(start, doc.Lines[0].StartMs+doc.OffsetMs-trimLyricGapMs)
		end = min(end, total-(doc.Lines[len(doc.Lines)-1].EndMs+doc.OffsetMs)-trimLyricGapMs)
	}
	trim := tools.Trim{TotalMs: total}
	if start >= trimMinMs {
		trim.StartMs = start
	}
	if end >= trimMinMs {
		trim.EndMs = end
	}
	if trim.StartMs+trim.EndMs >= total {
		return tools.Trim{}
	}
	slog.Info("cut silence", "song", id, "startMs", trim.StartMs, "endMs", trim.EndMs)
	return trim
}

func (m *Manager) stagePackage(ctx context.Context, id string) error {
	dir := m.TempDir(id)
	codec, kbps := m.Settings.Get(settings.AudioCodec), m.Settings.Int(settings.AudioBitrate)
	ext := tools.Extension(codec)
	out := filepath.Join(dir, "final"+ext)
	trim := m.planTrim(ctx, id, filepath.Join(dir, "instrumental.wav"))
	if err := tools.PackageAudio(ctx, filepath.Join(dir, "instrumental.wav"), out, codec, kbps, trim); err != nil {
		return err
	}
	key := media.AudioKey(id, ext)
	if err := m.storeFile(out, key); err != nil {
		return err
	}
	u := store.SongUpdate{AudioKey: &key, TrimStartMs: &trim.StartMs, TrimEndMs: &trim.EndMs}
	if m.Settings.Bool(settings.KeepGuide) && fileExists(filepath.Join(dir, "vocals.wav")) {
		guide := filepath.Join(dir, "guide"+ext)
		if err := tools.PackageAudio(ctx, filepath.Join(dir, "vocals.wav"), guide, codec, kbps, trim); err != nil {
			return err
		}
		gk := media.GuideKey(id, ext)
		if err := m.storeFile(guide, gk); err != nil {
			return err
		}
		u.GuideKey = &gk
	}
	if m.Settings.Get(settings.VideoKeep) != "off" {
		vk := media.VideoKey(id)
		if err := m.storeFile(filepath.Join(dir, "video.mp4"), vk); err != nil {
			return err
		}
		u.VideoKey = &vk
	} else if song, err := m.DB.GetSong(ctx, id); err == nil && song.VideoKey != "" {
		_ = m.Media.Delete(song.VideoKey) // left from before videos became temporary
		u.VideoKey = ptr("")
	}
	if err := m.DB.UpdateSong(ctx, id, u); err != nil {
		return err
	}
	if err := m.setState(ctx, id, StateKaraokeReady); err != nil {
		return err
	}
	_ = os.Remove(out)
	_ = os.Remove(filepath.Join(dir, "source.mp4"))
	if active, _ := m.DB.IsSongActive(ctx, id); !active {
		_ = os.Remove(filepath.Join(dir, "video.mp4"))
		_ = os.Remove(filepath.Join(dir, "quick.m4a"))
	}
	return nil
}

// aligned is what the helper writes after timing lyrics.
type aligned struct {
	Lines []struct {
		Words []lyrics.Word `json:"words"`
	} `json:"lines"`
}

func (m *Manager) stageAlign(ctx context.Context, id string) error {
	if err := m.setState(ctx, id, StateAligning); err != nil {
		return err
	}
	dir, err := filepath.Abs(m.TempDir(id))
	if err != nil {
		return err
	}
	song, err := m.DB.GetSong(ctx, id)
	if err != nil {
		return err
	}
	// Timing listens to the singer, so a karaoke source keeps its line timing.
	if !song.KaraokeVersion && !fileExists(filepath.Join(dir, skipAlignMarker)) {
		if err := m.timeLyrics(ctx, id, dir); err != nil {
			return err
		}
	}
	if err := m.setState(ctx, id, StateComplete); err != nil {
		return err
	}
	m.cleanupWavs(id)
	m.finishTemp(ctx, id)
	return nil
}

func (m *Manager) timeLyrics(ctx context.Context, id, dir string) error {
	doc, err := m.LoadLyrics(ctx, id)
	if err != nil {
		return err
	}
	if doc != nil && doc.MostlyFromVideo() {
		slog.Info("keeping the video's timing", "song", id, "exact", doc.ExactLines(), "lines", len(doc.Lines))
		return nil
	}
	vocals := filepath.Join(dir, "vocals.wav")
	modelDir, err := filepath.Abs(m.modelDir())
	if err != nil {
		return err
	}
	base := sidecar.Request{
		"input": vocals, "modelDir": modelDir, "device": m.device(id),
		"model": m.Settings.Get(settings.WhisperModel), "threads": m.Settings.Int(settings.MLThreads),
	}
	progress := func(p float64) { m.progress(id, StageAlign, p) }
	output := filepath.Join(dir, "timed.json")
	base["output"] = output

	if doc == nil || len(doc.Lines) == 0 {
		base["op"] = "transcribe"
		if _, err := m.Sidecar.Call(ctx, base, progress); err != nil {
			return err
		}
		res, err := readAligned(output)
		if err != nil {
			return err
		}
		doc = &lyrics.Doc{Version: 1, Source: "auto", Timing: "word", Voices: []lyrics.Voice{}}
		for _, l := range res.Lines {
			if len(l.Words) > 0 {
				doc.Lines = append(doc.Lines, lyrics.Line{StartMs: l.Words[0].StartMs, EndMs: l.Words[len(l.Words)-1].EndMs, Words: l.Words})
			}
		}
		return m.SaveLyrics(ctx, id, doc)
	}

	input := filepath.Join(dir, "lines.json")
	texts := make([]string, len(doc.Lines))
	for i, l := range doc.Lines {
		texts[i] = l.Text()
	}
	raw, _ := json.Marshal(map[string]any{"lines": texts})
	if err := os.WriteFile(input, raw, 0o644); err != nil {
		return err
	}
	base["op"], base["lyrics"], base["language"] = "align", input, doc.Language
	ev, err := m.Sidecar.Call(ctx, base, progress)
	if err != nil {
		return err
	}
	if ev.Confidence < 0.4 {
		slog.Info("keeping line timing, low confidence", "song", id, "confidence", ev.Confidence)
		return nil
	}
	res, err := readAligned(output)
	if err != nil {
		return err
	}
	if len(res.Lines) != len(doc.Lines) {
		return errors.New("timing returned a different number of lines")
	}
	before := slices.Clone(doc.Lines)
	for i := range doc.Lines {
		w := res.Lines[i].Words
		if len(w) == 0 || doc.Lines[i].Exact {
			continue
		}
		doc.Lines[i].Words = w
		doc.Lines[i].StartMs, doc.Lines[i].EndMs = w[0].StartMs, w[len(w)-1].EndMs
	}
	if n := lyrics.RepairTiming(doc.Lines, before); n > 0 {
		slog.Info("repaired lines word timing moved too far", "song", id, "lines", n)
	}
	lyrics.FitUntimed(doc.Lines)
	doc.Timing = "word"
	return m.SaveLyrics(ctx, id, doc)
}

func readAligned(path string) (*aligned, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a aligned
	return &a, json.Unmarshal(raw, &a)
}
