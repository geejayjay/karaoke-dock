package api

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"karaokedock/internal/admin"
	"karaokedock/internal/jobs"
	"karaokedock/internal/lyrics"
	"karaokedock/internal/player"
	"karaokedock/internal/settings"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
)

const sessionCookie = "kd_admin"

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// adminOnly applies the LAN rule, Origin check and session check.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return s.adminNetwork(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.Admin.Valid(r.Context(), c.Value) {
			writeErr(w, http.StatusUnauthorized, "Please sign in.")
			return
		}
		h(w, r)
	})
}

func (s *Server) adminNetwork(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ip := s.clientIP(r); !s.Settings.Bool(settings.AdminRemote) && (ip == nil || !(ip.IsLoopback() || ip.IsPrivate())) {
			writeErr(w, http.StatusForbidden, "The admin panel is only available on the local network.")
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			writeErr(w, http.StatusForbidden, "Request blocked.")
			return
		}
		h(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	a := s.adminOnly
	mux.HandleFunc("POST /api/admin/login", s.adminNetwork(s.adminLogin))
	mux.HandleFunc("POST /api/admin/logout", a(s.adminLogout))
	mux.HandleFunc("POST /api/admin/password", a(s.adminPassword))
	mux.HandleFunc("GET /api/admin/settings", a(s.adminGetSettings))
	mux.HandleFunc("PATCH /api/admin/settings", a(s.adminPatchSettings))
	mux.HandleFunc("GET /api/admin/status", a(s.adminStatus))
	mux.HandleFunc("POST /api/admin/storage/move", a(s.adminMove))
	mux.HandleFunc("GET /api/admin/backup", a(s.adminBackup))
	mux.HandleFunc("GET /api/admin/songs", a(s.adminSongs))
	mux.HandleFunc("POST /api/admin/songs/upload", a(s.adminUploadSong))
	mux.HandleFunc("GET /api/admin/songs/{id}", a(s.adminGetSong))
	mux.HandleFunc("PATCH /api/admin/songs/{id}", a(s.adminEditSong))
	mux.HandleFunc("DELETE /api/admin/songs/{id}", a(s.adminDeleteSong))
	mux.HandleFunc("POST /api/admin/songs/{id}/reprocess", a(s.adminReprocess))
	mux.HandleFunc("GET /api/admin/songs/{id}/identify", a(s.adminSearchIdentify))
	mux.HandleFunc("POST /api/admin/songs/{id}/reidentify", a(s.adminReidentifySong))
	mux.HandleFunc("POST /api/admin/songs/{id}/cover", a(s.adminUploadSongCover))
	mux.HandleFunc("POST /api/admin/songs/{id}/check-video", a(s.adminCheckVideo))
	mux.HandleFunc("DELETE /api/admin/songs/{id}/check-video", a(s.adminEndCheck))
	mux.HandleFunc("GET /api/admin/artists", a(s.adminArtists))
	mux.HandleFunc("GET /api/admin/artists/{id}", a(s.adminGetArtist))
	mux.HandleFunc("PATCH /api/admin/artists/{id}", a(s.adminEditArtist))
	mux.HandleFunc("POST /api/admin/artists/{id}/cover", a(s.adminUploadArtistCover))
	mux.HandleFunc("DELETE /api/admin/artists/{id}/cover", a(s.adminDeleteArtistCover))
	mux.HandleFunc("GET /api/admin/albums", a(s.adminAlbums))
	mux.HandleFunc("GET /api/admin/albums/{id}", a(s.adminGetAlbum))
	mux.HandleFunc("PATCH /api/admin/albums/{id}", a(s.adminEditAlbum))
	mux.HandleFunc("POST /api/admin/albums/{id}/cover", a(s.adminUploadAlbumCover))
	mux.HandleFunc("DELETE /api/admin/albums/{id}/cover", a(s.adminDeleteAlbumCover))
	mux.HandleFunc("GET /api/admin/processing", a(s.adminProcessing))
	mux.HandleFunc("GET /api/admin/processing/counts", a(s.adminProcessingCounts))
	mux.HandleFunc("GET /api/admin/logs", a(s.adminLogs))
	mux.HandleFunc("POST /api/admin/tools/yt-dlp/update", a(s.adminUpdateYtDlp))
	mux.HandleFunc("POST /api/admin/tools/amll/update", a(s.adminUpdateAMLL))
	mux.HandleFunc("POST /api/admin/restart", a(s.adminRestart))
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	token, err := s.Admin.Login(r.Context(), body.Password)
	if errors.Is(err, admin.ErrThrottled) {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Wrong password.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: s.isHTTPS(r), MaxAge: int(admin.SessionLifetime().Seconds())})
	writeJSON(w, 200, map[string]bool{"mustChange": s.Admin.MustChange()})
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Admin.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if err := s.Admin.ChangePassword(r.Context(), body.Current, body.Next); err != nil {
		writeErr(w, http.StatusBadRequest, capitalize(err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func (s *Server) adminGetSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"defs": settings.Defs, "values": s.Settings.All(), "mustChange": s.Admin.MustChange()})
}

func (s *Server) adminPatchSettings(w http.ResponseWriter, r *http.Request) {
	var kv map[string]string
	if readJSON(r, &kv) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if _, moved := kv[settings.MediaRoot]; moved && kv[settings.MediaRoot] != s.Settings.Get(settings.MediaRoot) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"fields": map[string]string{settings.MediaRoot: "Use Move files to change this folder."}})
		return
	}
	if t, ok := kv[settings.TempRoot]; ok && t != s.Settings.Get(settings.TempRoot) {
		if !filepath.IsAbs(t) || os.MkdirAll(t, 0o755) != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"fields": map[string]string{settings.TempRoot: "Enter a full folder path that can be created."}})
			return
		}
	}
	fieldErrs, restart, err := s.Settings.Patch(r.Context(), kv)
	if err != nil {
		fail(w, err)
		return
	}
	if len(fieldErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"fields": fieldErrs})
		return
	}
	s.Player.Broadcast("settings.playback", map[string]any{"playback": playbackFrom(s.Settings), "joinUrl": s.Player.JoinURL()})
	writeJSON(w, 200, map[string]any{"restart": restart})
}

func playbackFrom(c *settings.Manager) map[string]any {
	return player.PlaybackSettings(c)
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	disk := func(p string) map[string]uint64 {
		free, total, err := tools.DiskUsage(p)
		if err != nil {
			return nil
		}
		return map[string]uint64{"free": free, "total": total}
	}
	temp := s.Settings.Get(settings.TempRoot)
	writeJSON(w, 200, map[string]any{
		"tools":      tools.Versions(ctx),
		"encoders":   s.Encoders.Available(),
		"sidecar":    map[string]any{"running": s.Sidecar.Running(), "devices": s.Sidecar.Devices()},
		"network":    s.Player.NetworkStatus(),
		"joinUrl":    s.Player.JoinURL(),
		"hostPin":    s.Settings.Get(settings.HostPin),
		"mediaRoot":  s.Media.Root(),
		"move":       s.Jobs.CurrentMove(),
		"disk":       map[string]any{"metadata": disk(s.MetadataDir), "media": disk(s.Media.Root()), "temp": disk(temp)},
		"mustChange": s.Admin.MustChange(),
	})
}

func (s *Server) adminMove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if readJSON(r, &body) != nil || !filepath.IsAbs(body.Target) {
		writeErr(w, http.StatusBadRequest, "Enter a full folder path.")
		return
	}
	// The job outlives this request.
	if err := s.Jobs.StartMove(context.WithoutCancel(r.Context()), body.Target); err != nil {
		writeErr(w, http.StatusBadRequest, capitalize(err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// adminBackup streams a zip of the metadata and all lyrics.
func (s *Server) adminBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tmp := filepath.Join(s.Settings.Get(settings.TempRoot), ".backup-"+randomID(6)+".db")
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		fail(w, err)
		return
	}
	defer os.Remove(tmp)
	if _, err := s.DB.W.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="karaokedock-backup-%s.zip"`, time.Now().Format("2006-01-02")))
	zw := zip.NewWriter(w)
	defer zw.Close()
	if err := addZipFile(zw, "metadata/karaokedock.db", tmp); err != nil {
		slog.Error("backup", "err", err)
		return
	}
	for _, sub := range []string{"songs", "covers"} {
		root := filepath.Join(s.MetadataDir, sub)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // skip missing folders
			}
			rel, err := filepath.Rel(s.MetadataDir, p)
			if err != nil {
				return nil //nolint:nilerr
			}
			return addZipFile(zw, "metadata/"+filepath.ToSlash(rel), p)
		})
	}
	if err := s.DB.LyricKeys(ctx, func(key string) error {
		f, err := s.Media.Open(key)
		if err != nil {
			slog.Warn("backup missing lyrics", "key", key, "err", err)
			return nil
		}
		defer f.Close()
		out, err := zw.Create("media/" + key)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	}); err != nil {
		slog.Error("backup", "err", err)
	}
}

func addZipFile(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, f)
	return err
}

// adminSongs lists the library. With missing=1, it keeps reading pages until it
// has a full page of songs whose audio is gone, or runs out of songs.
const maxSongUpload = 2 << 30

// adminUploadSong imports an audio or video file, which needs no internet.
func (s *Server) adminUploadSong(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSongUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Choose an audio or video file.")
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FileName() == "" {
		writeErr(w, http.StatusBadRequest, "Choose an audio or video file.")
		return
	}
	ctx := r.Context()
	id := jobs.FilePrefix + randomID(8)
	dir := s.Jobs.TempDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, err)
		return
	}
	upload := filepath.Join(dir, "upload")
	f, err := os.Create(upload)
	if err != nil {
		fail(w, err)
		return
	}
	_, err = io.Copy(f, part)
	f.Close()
	if err != nil {
		_ = os.RemoveAll(dir)
		writeErr(w, http.StatusBadRequest, "The upload stopped. Try again.")
		return
	}
	source := filepath.Join(dir, "source.mp4")
	if err := tools.ToSourceVideo(ctx, upload, source); err != nil {
		slog.Warn("file import", "err", err)
		_ = os.RemoveAll(dir)
		writeErr(w, http.StatusBadRequest, "That file couldn't be read. Try another one.")
		return
	}
	_ = os.Remove(upload)
	durationMs, err := tools.DurationMs(ctx, source)
	if err != nil {
		_ = os.RemoveAll(dir)
		writeErr(w, http.StatusBadRequest, "That file couldn't be read. Try another one.")
		return
	}
	name := filepath.Base(part.FileName())
	title := strings.TrimSuffix(name, filepath.Ext(name))
	info, _ := json.Marshal(tools.Info{ID: id, Title: title, Duration: float64(durationMs) / 1000})
	if err := os.WriteFile(filepath.Join(dir, "source.info.json"), info, 0o644); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.DB.InsertSong(ctx, id, lyrics.CleanTitle(title)); err != nil {
		fail(w, err)
		return
	}
	if err := s.DB.UpdateSong(ctx, id, store.SongUpdate{DurationMs: &durationMs}); err != nil {
		fail(w, err)
		return
	}
	if err := s.Jobs.ImportFile(ctx, id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Server) adminCheckVideo(w http.ResponseWriter, r *http.Request) {
	err := s.Jobs.CheckVideo(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]bool{"ok": true})
	case errors.Is(err, jobs.ErrNeedsFile):
		writeErr(w, http.StatusBadRequest, "This song came from a file, so there's no video.")
	default:
		slog.Warn("check video", "song", r.PathValue("id"), "err", err)
		writeErr(w, http.StatusBadGateway, "Couldn't get the video.")
	}
}

func (s *Server) adminEndCheck(w http.ResponseWriter, r *http.Request) {
	s.Jobs.EndCheck(r.Context(), r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// statusRow is a song with its audio and lyrics progress.
type statusRow struct {
	store.SongRow
	AlbumID  string             `json:"albumId,omitempty"`
	Genre    string             `json:"genre,omitempty"`
	Language string             `json:"language,omitempty"`
	Artists  []store.SongArtist `json:"artists,omitempty"`
	jobs.SongStatus
}

func (s *Server) adminProcessing(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListProcessing(r.Context(), q.Get("filter"), q.Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	out := store.Page[statusRow]{Items: make([]statusRow, 0, len(page.Items)), NextCursor: page.NextCursor}
	for i := range page.Items {
		song := &page.Items[i]
		out.Items = append(out.Items, statusRow{
			SongRow:    song.Row(),
			AlbumID:    song.AlbumID,
			Genre:      song.Genre,
			Language:   song.Language,
			SongStatus: s.Jobs.Status(song),
		})
	}
	writeJSON(w, 200, out)
}

// writeLibrary sends a page of library songs with their progress.
func (s *Server) writeLibrary(w http.ResponseWriter, r *http.Request, page *store.Page[store.SongRow]) {
	ids := make([]string, len(page.Items))
	for i, row := range page.Items {
		ids[i] = row.ID
	}
	artistsBySong, _ := s.DB.SongArtistsBySongIDs(r.Context(), ids)
	out := store.Page[statusRow]{Items: make([]statusRow, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, row := range page.Items {
		song, err := s.DB.GetSong(r.Context(), row.ID)
		if err != nil {
			continue // deleted since the page was read
		}
		out.Items = append(out.Items, statusRow{
			SongRow:    row,
			AlbumID:    song.AlbumID,
			Genre:      song.Genre,
			Language:   song.Language,
			Artists:    artistsBySong[row.ID],
			SongStatus: s.Jobs.Status(song),
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) adminProcessingCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.DB.CountProcessing(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, counts)
}

func (s *Server) adminSongs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := queryLimit(r)
	baseQuery := store.SongQuery{
		Q:            q.Get("q"),
		Artist:       q.Get("artist"),
		Album:        q.Get("album"),
		Genre:        q.Get("genre"),
		Language:     q.Get("language"),
		Unidentified: q.Get("unidentified") == "1",
		Cursor:       q.Get("cursor"),
		Limit:        limit,
	}
	if q.Get("missing") != "1" {
		page, err := s.DB.ListSongs(r.Context(), baseQuery)
		if err != nil {
			fail(w, err)
			return
		}
		s.writeLibrary(w, r, page)
		return
	}
	out := store.Page[store.SongRow]{Items: []store.SongRow{}}
	cursor := q.Get("cursor")
	for {
		mq := baseQuery
		mq.Cursor = cursor
		mq.Limit = store.MaxLimit
		mq.OnlyMissing = true
		page, err := s.DB.ListSongs(r.Context(), mq)
		if err != nil {
			fail(w, err)
			return
		}
		for i, row := range page.Items {
			song, err := s.DB.GetSong(r.Context(), row.ID)
			if err != nil {
				continue
			}
			if _, err := s.Media.Stat(song.AudioKey); err == nil {
				continue
			}
			out.Items = append(out.Items, row)
			if len(out.Items) == limit {
				if i+1 < len(page.Items) || page.NextCursor != nil {
					c := store.EncodeCursor(store.SortKey(row.Title), row.ID)
					out.NextCursor = &c
				}
				s.writeLibrary(w, r, &out)
				return
			}
		}
		if page.NextCursor == nil {
			s.writeLibrary(w, r, &out)
			return
		}
		cursor = *page.NextCursor
	}
}

func (s *Server) adminGetSong(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	song, err := s.DB.GetSong(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	artists, _ := s.DB.SongArtistsBySongIDs(r.Context(), []string{id})
	writeJSON(w, 200, struct {
		*store.Song
		CoverID string             `json:"coverId,omitempty"`
		Artists []store.SongArtist `json:"artists,omitempty"`
	}{
		Song:    song,
		CoverID: song.CoverID(),
		Artists: artists[id],
	})
}

func (s *Server) adminEditSong(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title         string `json:"title"`
		Artist        string `json:"artist"`
		Album         string `json:"album"`
		Genre         string `json:"genre"`
		Language      string `json:"language"`
		RefreshLyrics bool   `json:"refreshLyrics"`
	}
	if readJSON(r, &body) != nil || strings.TrimSpace(body.Title) == "" {
		writeErr(w, http.StatusBadRequest, "A song needs a title.")
		return
	}
	ctx := r.Context()
	old, err := s.DB.GetSong(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	oldArtists, _ := s.DB.SongArtistsBySongIDs(ctx, []string{id})
	var artists []store.ArtistInput
	for a := range strings.SplitSeq(body.Artist, ",") {
		if a = strings.TrimSpace(a); a != "" {
			in := store.ArtistInput{Name: a}
			for _, oa := range oldArtists[id] {
				if strings.EqualFold(oa.Name, a) {
					in.MBID, in.Gender = oa.ID, oa.Gender
					break
				}
			}
			artists = append(artists, in)
		}
	}
	var album *store.AlbumInput
	if t := strings.TrimSpace(body.Album); t != "" {
		album = &store.AlbumInput{Title: t}
		if strings.EqualFold(old.Album, t) && old.AlbumID != "" {
			if a, err := s.DB.GetAlbum(ctx, old.AlbumID); err == nil {
				album = &store.AlbumInput{ID: a.ID, Title: t, Year: a.Year, CoverKey: a.CoverKey}
			}
		}
	}
	title := strings.TrimSpace(body.Title)
	genre := strings.TrimSpace(body.Genre)
	lang := strings.TrimSpace(body.Language)
	if err := s.DB.UpdateSong(ctx, id, store.SongUpdate{Title: &title, Genre: &genre, Language: &lang}); err != nil {
		fail(w, err)
		return
	}
	if err := s.DB.SetSongDetails(ctx, id, artists, album); err != nil {
		fail(w, err)
		return
	}
	_ = s.Jobs.SyncSongCover(ctx, id)
	if body.RefreshLyrics {
		var metaArtists []lyrics.Artist
		for _, a := range artists {
			metaArtists = append(metaArtists, lyrics.Artist{MBID: a.MBID, Name: a.Name, Gender: a.Gender})
		}
		_ = s.Jobs.RefreshLyrics(ctx, id, jobs.Meta{
			Title:      title,
			Artists:    metaArtists,
			Album:      strings.TrimSpace(body.Album),
			Genre:      genre,
			Language:   lang,
			YouTubeID:  id,
			DurationMs: old.DurationMs,
		})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminSearchIdentify(w http.ResponseWriter, r *http.Request) {
	if !s.Player.Online() {
		writeErr(w, http.StatusServiceUnavailable, "Searching song details needs internet.")
		return
	}
	id := r.PathValue("id")
	q := r.URL.Query()
	title := strings.TrimSpace(q.Get("title"))
	artist := strings.TrimSpace(q.Get("artist"))
	if title == "" && artist == "" {
		if song, err := s.DB.GetSong(r.Context(), id); err == nil {
			title, artist = song.Title, song.Artist
		}
	}
	firstArtist := artist
	if i := strings.Index(firstArtist, ","); i >= 0 {
		firstArtist = strings.TrimSpace(firstArtist[:i])
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	items, err := lyrics.SearchRecordings(ctx, firstArtist, title)
	if err != nil {
		slog.Warn("identify search", "song", id, "err", err)
		writeErr(w, http.StatusBadGateway, "Couldn't search song details right now.")
		return
	}
	if items == nil {
		items = []lyrics.Details{}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminReidentifySong(w http.ResponseWriter, r *http.Request) {
	if !s.Player.Online() {
		writeErr(w, http.StatusServiceUnavailable, "Re-identifying needs internet.")
		return
	}
	id := r.PathValue("id")
	var req jobs.ReidentifyReq
	_ = readJSON(r, &req)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	song, err := s.Jobs.Reidentify(ctx, id, req)
	if err != nil {
		if errors.Is(err, jobs.ErrNoMatch) {
			writeErr(w, http.StatusNotFound, "No matching song was found. Try editing the title or singer first.")
			return
		}
		fail(w, err)
		return
	}
	artists, _ := s.DB.SongArtistsBySongIDs(r.Context(), []string{id})
	writeJSON(w, 200, struct {
		*store.Song
		CoverID string             `json:"coverId,omitempty"`
		Artists []store.SongArtist `json:"artists,omitempty"`
	}{
		Song:    song,
		CoverID: song.CoverID(),
		Artists: artists[id],
	})
}

const maxCoverUpload = 16 << 20

func readMultipartFile(w http.ResponseWriter, r *http.Request) (io.ReadCloser, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCoverUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Choose an image file.")
		return nil, false
	}
	part, err := mr.NextPart()
	if err != nil || part.FileName() == "" {
		writeErr(w, http.StatusBadRequest, "Choose an image file.")
		return nil, false
	}
	return part, true
}

func (s *Server) adminUploadSongCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	song, err := s.DB.GetSong(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	part, ok := readMultipartFile(w, r)
	if !ok {
		return
	}
	defer part.Close()
	albumID := song.AlbumID
	if albumID == "" {
		songArtists, _ := s.DB.SongArtistsBySongIDs(ctx, []string{id})
		var inputs []store.ArtistInput
		for _, a := range songArtists[id] {
			inputs = append(inputs, store.ArtistInput{MBID: a.ID, Name: a.Name, Gender: a.Gender})
		}
		albumTitle := song.Album
		if albumTitle == "" {
			albumTitle = song.Title
		}
		firstID := ""
		if len(inputs) > 0 {
			firstID = inputs[0].MBID
		}
		albumID = store.HashID(firstID, albumTitle)
		if err := s.DB.SetSongDetails(ctx, id, inputs, &store.AlbumInput{ID: albumID, Title: albumTitle}); err != nil {
			fail(w, err)
			return
		}
	}
	if err := tools.SaveCoverImage(ctx, part, s.Jobs.CoverPath(albumID)); err != nil {
		slog.Warn("song cover upload", "song", id, "err", err)
		writeErr(w, http.StatusBadRequest, "That image couldn't be read. Try a JPG or PNG file.")
		return
	}
	if err := s.Jobs.RefreshAlbumCover(ctx, albumID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"coverId": albumID})
}

func (s *Server) adminArtists(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListArtists(r.Context(), q.Get("q"), q.Get("from"), q.Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) adminGetArtist(w http.ResponseWriter, r *http.Request) {
	a, err := s.DB.GetArtist(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminEditArtist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name   string `json:"name"`
		Gender string `json:"gender"`
	}
	if readJSON(r, &body) != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "Enter a singer name.")
		return
	}
	gender := strings.ToLower(strings.TrimSpace(body.Gender))
	if gender != "" && gender != "male" && gender != "female" {
		gender = ""
	}
	if err := s.DB.UpdateArtist(r.Context(), id, strings.TrimSpace(body.Name), gender); err != nil {
		fail(w, err)
		return
	}
	a, err := s.DB.GetArtist(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminUploadArtistCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "Bad artist ID.")
		return
	}
	if _, err := s.DB.GetArtist(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	part, ok := readMultipartFile(w, r)
	if !ok {
		return
	}
	defer part.Close()
	coverID := "artist-" + id
	if err := tools.SaveCoverImage(r.Context(), part, s.Jobs.CoverPath(coverID)); err != nil {
		slog.Warn("artist cover upload", "artist", id, "err", err)
		writeErr(w, http.StatusBadRequest, "That image couldn't be read. Try a JPG or PNG file.")
		return
	}
	if err := s.DB.SetArtistCover(r.Context(), id, "covers/"+coverID+".jpg"); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"coverId": coverID})
}

func (s *Server) adminDeleteArtistCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "Bad artist ID.")
		return
	}
	_ = os.Remove(s.Jobs.CoverPath("artist-" + id))
	if err := s.DB.SetArtistCover(r.Context(), id, ""); err != nil {
		fail(w, err)
		return
	}
	a, err := s.DB.GetArtist(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminAlbums(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListAlbums(r.Context(), q.Get("q"), q.Get("artist"), q.Get("from"), q.Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) adminGetAlbum(w http.ResponseWriter, r *http.Request) {
	a, err := s.DB.GetAlbum(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminEditAlbum(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
	}
	if readJSON(r, &body) != nil || strings.TrimSpace(body.Title) == "" {
		writeErr(w, http.StatusBadRequest, "Enter an album title.")
		return
	}
	if body.Year < 0 || body.Year > 3000 {
		body.Year = 0
	}
	if err := s.DB.UpdateAlbum(r.Context(), id, strings.TrimSpace(body.Title), body.Year); err != nil {
		fail(w, err)
		return
	}
	a, err := s.DB.GetAlbum(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminUploadAlbumCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "Bad album ID.")
		return
	}
	if _, err := s.DB.GetAlbum(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	part, ok := readMultipartFile(w, r)
	if !ok {
		return
	}
	defer part.Close()
	if err := tools.SaveCoverImage(r.Context(), part, s.Jobs.CoverPath(id)); err != nil {
		slog.Warn("album cover upload", "album", id, "err", err)
		writeErr(w, http.StatusBadRequest, "That image couldn't be read. Try a JPG or PNG file.")
		return
	}
	if err := s.Jobs.RefreshAlbumCover(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"coverId": id})
}

func (s *Server) adminDeleteAlbumCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "Bad album ID.")
		return
	}
	_ = os.Remove(s.Jobs.CoverPath(id))
	if err := s.Jobs.RefreshAlbumCover(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminDeleteSong(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	song, err := s.DB.GetSong(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	if active, _ := s.DB.IsSongActive(ctx, id); active {
		writeErr(w, http.StatusConflict, "This song is playing. Skip it first.")
		return
	}
	if err := s.DB.DeleteSong(ctx, id); err != nil {
		fail(w, err)
		return
	}
	for _, k := range []string{song.AudioKey, song.LyricsKey, song.VideoKey, song.GuideKey} {
		if k != "" {
			_ = s.Media.Delete(k)
		}
	}
	_ = os.RemoveAll(s.Jobs.SongDir(id))
	_ = os.RemoveAll(s.Jobs.TempDir(id))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminReprocess(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.PathValue("id"), jobs.FilePrefix) && !s.Player.Online() {
		writeErr(w, http.StatusServiceUnavailable, "Reprocessing needs internet.")
		return
	}
	if err := s.Jobs.Reprocess(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, jobs.ErrNeedsFile) {
			writeErr(w, http.StatusBadRequest, "Add the file again to reprocess it.")
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	writeJSON(w, 200, s.Logs.Page(before, queryLimit(r)))
}

func (s *Server) adminUpdateYtDlp(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	out, err := tools.UpdateYtDlp(ctx)
	if err != nil {
		slog.Warn("yt-dlp update", "err", err, "output", out)
		writeErr(w, http.StatusBadGateway, "Update failed. Check the logs.")
		return
	}
	writeJSON(w, 200, map[string]string{"output": out})
}

func (s *Server) adminUpdateAMLL(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	dir := filepath.Join(s.MetadataDir, "amll")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		writeErr(w, http.StatusBadRequest, "The duet lyric index isn't set up. Run setup first.")
		return
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("amll update", "err", err, "output", string(out))
		writeErr(w, http.StatusBadGateway, "Update failed. Check the logs.")
		return
	}
	s.AMLL.Rebuild()
	writeJSON(w, 200, map[string]string{"output": strings.TrimSpace(string(out))})
}

func (s *Server) adminRestart(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.Restart()
	}()
}

// LogEntry is one line in the admin log view.
type LogEntry struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// LogBuffer keeps recent logs in memory.
type LogBuffer struct {
	mu   sync.Mutex
	rows []LogEntry
	seq  int64
}

const logCapacity = 5000

// Write stores one log line. It implements io.Writer for slog.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.rows = append(b.rows, LogEntry{b.seq, strings.TrimRight(string(p), "\n")})
	if len(b.rows) > logCapacity {
		b.rows = append(b.rows[:0], b.rows[len(b.rows)-logCapacity:]...)
	}
	return len(p), nil
}

// Page returns entries older than before, newest first.
func (b *LogBuffer) Page(before int64, limit int) store.Page[LogEntry] {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := store.Page[LogEntry]{Items: []LogEntry{}}
	for i := len(b.rows) - 1; i >= 0; i-- {
		e := b.rows[i]
		if before > 0 && e.Seq >= before {
			continue
		}
		if len(out.Items) == limit {
			c := strconv.FormatInt(out.Items[limit-1].Seq, 10)
			out.NextCursor = &c
			break
		}
		out.Items = append(out.Items, e)
	}
	return out
}
