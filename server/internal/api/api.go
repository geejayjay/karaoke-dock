// Package api serves the HTTP routes, WebSocket and frontend files.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"karaokedock/internal/admin"
	"karaokedock/internal/jobs"
	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/player"
	"karaokedock/internal/settings"
	"karaokedock/internal/sidecar"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
)

// Server holds the route dependencies.
type Server struct {
	DB          *store.Store
	Settings    *settings.Manager
	Jobs        *jobs.Manager
	Player      *player.Player
	Media       *media.Fs
	Admin       *admin.Service
	Sidecar     *sidecar.Client
	Encoders    *tools.Encoders
	AMLL        *lyrics.AMLL
	MetadataDir string
	Web         fs.FS
	Logs        *LogBuffer
	Restart     func()

	pin    throttle
	mu     sync.Mutex
	limits map[string][]time.Time

	reacted map[string]time.Time
}

type throttle struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

func (t *throttle) allow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Now().After(t.until)
}

func (t *throttle) fail() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if t.failures >= 5 {
		t.until = time.Now().Add(min(time.Second<<min(t.failures-5, 8), 5*time.Minute))
	}
}

func (t *throttle) ok() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures, t.until = 0, time.Time{}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// fail answers a store error.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "Not found.")
	case errors.Is(err, store.ErrBadCursor):
		writeErr(w, http.StatusBadRequest, "Bad page cursor.")
	case errors.Is(err, store.ErrBadFilter):
		writeErr(w, http.StatusBadRequest, "Bad filter.")
	case errors.Is(err, context.Canceled):
	default:
		slog.Error("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "Something went wrong.")
	}
}

func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return store.ClampLimit(n)
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	s.limits = map[string][]time.Time{}
	mux := http.NewServeMux()

	// Guest routes need the room code.
	room := s.requireRoom
	host := func(h http.HandlerFunc) http.HandlerFunc { return s.requireRoom(s.requireHost(h)) }
	mux.HandleFunc("GET /api/session", room(s.session))
	mux.HandleFunc("POST /api/host/verify", host(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) }))
	mux.HandleFunc("GET /api/songs", room(s.listSongs))
	mux.HandleFunc("GET /api/artists", room(s.listArtists))
	mux.HandleFunc("GET /api/artists/{id}", room(s.getArtist))
	mux.HandleFunc("GET /api/albums", room(s.listAlbums))
	mux.HandleFunc("GET /api/albums/{id}", room(s.getAlbum))
	mux.HandleFunc("GET /api/genres", room(s.listDistinct("genre")))
	mux.HandleFunc("GET /api/languages", room(s.listDistinct("language")))
	mux.HandleFunc("GET /api/youtube/search", room(s.youtubeSearch))
	mux.HandleFunc("POST /api/songs/import", room(s.importSong))
	mux.HandleFunc("GET /api/songs/{id}/lyrics", s.getLyrics)
	mux.HandleFunc("POST /api/songs/{id}/offset", host(s.setOffset))
	mux.HandleFunc("POST /api/songs/{id}/voices", host(s.setVoices))
	mux.HandleFunc("POST /api/songs/{id}/karaoke", host(s.setKaraokeLabel))
	mux.HandleFunc("GET /api/queue", room(s.listQueue))
	mux.HandleFunc("GET /api/queue/songs", room(s.queuedSongs))
	s.partyRoutes(mux, room, host)
	mux.HandleFunc("GET /api/queue/mine", room(s.myTurn))
	mux.HandleFunc("GET /api/history", room(s.singAgain))
	mux.HandleFunc("POST /api/queue", room(s.addQueue))
	mux.HandleFunc("DELETE /api/queue/{id}", room(s.removeQueue))
	mux.HandleFunc("POST /api/queue/{id}/move", host(s.moveQueue))
	mux.HandleFunc("POST /api/player/{action}", host(s.playerAction))
	mux.HandleFunc("GET /ws", s.websocket)
	mux.HandleFunc("GET /api/qr.png", s.qr)
	mux.HandleFunc("GET /api/qr.svg", s.qrSVG)

	// Media.
	mux.HandleFunc("GET /media/{id}/{file}", s.serveSongFile)
	mux.HandleFunc("GET /covers/{id}", s.serveCover)
	mux.HandleFunc("GET /covers/{id}/palette", s.serveCoverPalette)

	s.adminRoutes(mux)
	s.webRoutes(mux)
	return mux
}

func (s *Server) requireRoom(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.Header.Get("X-Room")
		if code == "" {
			code = r.URL.Query().Get("room")
		}
		if !strings.EqualFold(code, s.Settings.Get(settings.RoomCode)) {
			writeErr(w, http.StatusForbidden, "Scan the QR code on the TV to join.")
			return
		}
		h(w, r)
	}
}

func (s *Server) requireHost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.pin.allow() {
			writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment.")
			return
		}
		if r.Header.Get("X-Host-Pin") != s.Settings.Get(settings.HostPin) {
			s.pin.fail()
			writeErr(w, http.StatusUnauthorized, "Wrong PIN.")
			return
		}
		s.pin.ok()
		h(w, r)
	}
}

const clientCookie = "kd_client"

func (s *Server) clientID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(clientCookie); err == nil && len(c.Value) >= 16 && len(c.Value) <= 64 {
		return c.Value
	}
	id := randomID(16)
	http.SetCookie(w, &http.Cookie{Name: clientCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: 30 * 24 * 3600, Secure: s.isHTTPS(r)})
	return id
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"clientTag": store.ClientTag(s.clientID(w, r)), "online": s.Player.Online()})
}

func (s *Server) listSongs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListSongs(r.Context(), store.SongQuery{
		Q: q.Get("q"), Artist: q.Get("artist"), Album: q.Get("album"), Genre: q.Get("genre"), Language: q.Get("language"),
		Sort: q.Get("sort"), From: q.Get("from"), Cursor: q.Get("cursor"), Limit: queryLimit(r),
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "unknown sort") {
			writeErr(w, http.StatusBadRequest, "Unknown sort.")
			return
		}
		fail(w, err)
		return
	}
	// Nothing matched exactly, so try for typos and words from the lyrics.
	if q.Get("q") != "" && q.Get("cursor") == "" && len(page.Items) == 0 {
		if page, err = s.DB.FuzzySongs(r.Context(), q.Get("q"), queryLimit(r)); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, 200, page)
}

func (s *Server) listArtists(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListArtists(r.Context(), q.Get("q"), q.Get("from"), q.Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) getArtist(w http.ResponseWriter, r *http.Request) {
	a, err := s.DB.GetArtist(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) listAlbums(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.DB.ListAlbums(r.Context(), q.Get("q"), q.Get("artist"), q.Get("from"), q.Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) getAlbum(w http.ResponseWriter, r *http.Request) {
	a, err := s.DB.GetAlbum(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) listDistinct(column string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := s.DB.ListDistinct(r.Context(), column, r.URL.Query().Get("cursor"), queryLimit(r))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, page)
	}
}

// YouTube search limits, smaller than library lists because each page is slow.
const (
	ytDefaultLimit = 20
	ytMaxLimit     = 50
)

func (s *Server) youtubeSearch(w http.ResponseWriter, r *http.Request) {
	if !s.Player.Online() {
		writeErr(w, http.StatusServiceUnavailable, "YouTube needs internet.")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "Type something to search.")
		return
	}
	if id := tools.ExtractVideoID(q); id != "" {
		items := s.withOwned(r.Context(), []tools.SearchResult{{VideoID: id, Title: "Add this link"}})
		writeJSON(w, 200, store.Page[searchRow]{Items: items})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = ytDefaultLimit
	}
	limit = min(limit, ytMaxLimit)
	start := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		v, _, err := store.DecodeCursor(c)
		if err != nil {
			fail(w, err)
			return
		}
		start, _ = strconv.Atoi(v)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	items, err := tools.Search(ctx, karaokeQuery(q), start, limit)
	if err != nil {
		slog.Warn("youtube search", "err", err)
		writeErr(w, http.StatusBadGateway, "YouTube search failed. Try again.")
		return
	}
	for i := range items {
		items[i].Karaoke = lyrics.IsKaraokeTitle(items[i].Title)
	}
	page := store.Page[searchRow]{Items: s.withOwned(r.Context(), items)}
	if len(items) == limit {
		c := store.EncodeCursor(strconv.Itoa(start+limit), "")
		page.NextCursor = &c
	}
	writeJSON(w, 200, page)
}

// karaokeQuery makes every YouTube search look for karaoke versions.
func karaokeQuery(q string) string {
	if lyrics.IsKaraokeTitle(q) {
		return q
	}
	return q + " karaoke"
}

// searchRow is a YouTube result. Song is set when we already have that song, and
// replaces everything YouTube said about it.
type searchRow struct {
	tools.SearchResult
	Song *store.SongRow `json:"song,omitempty"`
}

// withOwned swaps in our own details for songs already in the library. A song
// whose import failed doesn't count, so it can be picked again.
func (s *Server) withOwned(ctx context.Context, items []tools.SearchResult) []searchRow {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.VideoID
	}
	owned, err := s.DB.SongRowsByID(ctx, ids)
	if err != nil {
		slog.Warn("owned songs lookup", "err", err)
	}
	out := make([]searchRow, len(items))
	for i, it := range items {
		out[i] = searchRow{SearchResult: it}
		if song, ok := owned[it.VideoID]; ok && song.State != jobs.StateFailed {
			out[i].Song = &song
			out[i].Title, out[i].Channel, out[i].DurationMs, out[i].Karaoke = song.Title, song.Artist, song.DurationMs, song.Karaoke
		}
	}
	return out
}

// allowImport applies the per-guest hourly limit.
func (s *Server) allowImport(client string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-time.Hour)
	kept := s.limits[client][:0]
	for _, t := range s.limits[client] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= s.Settings.Int(settings.ImportLimit) {
		s.limits[client] = kept
		return false
	}
	s.limits[client] = append(kept, time.Now())
	return true
}

// ensureSong creates a song and starts its import when new.
func (s *Server) ensureSong(ctx context.Context, w http.ResponseWriter, client, id, title, channel string, durationMs int64) bool {
	if _, err := s.DB.GetSong(ctx, id); err == nil {
		return true
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, err)
		return false
	}
	if !s.allowImport(client) {
		writeErr(w, http.StatusTooManyRequests, "You've added a lot of new songs. Try again later.")
		return false
	}
	if title == "" {
		title = "New song"
	}
	if len(title) > 300 {
		title = title[:300]
	}
	created, err := s.DB.InsertSong(ctx, id, lyrics.CleanTitle(title))
	if err != nil {
		fail(w, err)
		return false
	}
	if created {
		if channel != "" { // only a search result has one; a pasted link doesn't
			s.Jobs.SaveSource(id, title, channel, durationMs)
		}
		if lyrics.IsKaraokeTitle(title) { // the stored title has this word removed
			yes := true
			if err := s.DB.UpdateSong(ctx, id, store.SongUpdate{KaraokeVersion: &yes}); err != nil {
				fail(w, err)
				return false
			}
		}
		if err := s.Jobs.Import(ctx, id); err != nil {
			fail(w, err)
			return false
		}
	}
	return true
}

func (s *Server) importSong(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VideoID    string `json:"videoId"`
		Title      string `json:"title"`
		Channel    string `json:"channel"`
		DurationMs int64  `json:"durationMs"`
	}
	if err := readJSON(r, &body); err != nil || !tools.VideoID.MatchString(body.VideoID) {
		writeErr(w, http.StatusBadRequest, "That isn't a YouTube video.")
		return
	}
	if !s.ensureSong(r.Context(), w, s.clientID(w, r), body.VideoID, body.Title, body.Channel, body.DurationMs) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": body.VideoID})
}

func (s *Server) listQueue(w http.ResponseWriter, r *http.Request) {
	page, err := s.DB.ListQueue(r.Context(), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	client := s.clientID(w, r)
	type row struct {
		store.QueueItem
		Mine bool `json:"mine"`
	}
	out := store.Page[row]{Items: make([]row, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, it := range page.Items {
		out.Items = append(out.Items, row{it, it.ClientID == client})
	}
	writeJSON(w, 200, out)
}

func (s *Server) myTurn(w http.ResponseWriter, r *http.Request) {
	item, ahead, err := s.DB.NextForClient(r.Context(), s.clientID(w, r))
	if err != nil {
		fail(w, err)
		return
	}
	if item == nil {
		writeJSON(w, 200, map[string]any{"waiting": false})
		return
	}
	writeJSON(w, 200, map[string]any{"waiting": true, "ahead": ahead, "title": item.Song.Title})
}

func (s *Server) singAgain(w http.ResponseWriter, r *http.Request) {
	page, err := s.DB.SungBefore(r.Context(), s.clientID(w, r), r.URL.Query().Get("cursor"), queryLimit(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) queuedSongs(w http.ResponseWriter, r *http.Request) {
	ids, err := s.DB.QueuedSongIDs(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string][]string{"ids": ids})
}

func (s *Server) addQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SongID     string `json:"songId"`
		VideoID    string `json:"videoId"`
		Title      string `json:"title"`
		Channel    string `json:"channel"`
		DurationMs int64  `json:"durationMs"`
		Singer     string `json:"singer"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	body.Singer = strings.TrimSpace(body.Singer)
	if body.Singer == "" || len(body.Singer) > 40 {
		writeErr(w, http.StatusBadRequest, "Enter a name up to 40 letters.")
		return
	}
	ctx := r.Context()
	client := s.clientID(w, r)
	n, err := s.DB.CountSinger(ctx, client)
	if err != nil {
		fail(w, err)
		return
	}
	if n >= s.Settings.Int(settings.QueueLimit) {
		writeErr(w, http.StatusTooManyRequests, "You have the most songs allowed in the queue.")
		return
	}
	id := body.SongID
	if id == "" {
		id = body.VideoID
		if !tools.VideoID.MatchString(id) {
			writeErr(w, http.StatusBadRequest, "That isn't a YouTube video.")
			return
		}
		if !s.ensureSong(ctx, w, client, id, body.Title, body.Channel, body.DurationMs) {
			return
		}
	} else if _, err := s.DB.GetSong(ctx, id); err != nil {
		fail(w, err)
		return
	}
	already, err := s.DB.SongQueued(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	if already {
		writeErr(w, http.StatusConflict, "That song is already in the queue.")
		return
	}
	itemID, err := s.DB.AddToQueue(ctx, id, body.Singer, client)
	if err != nil {
		fail(w, err)
		return
	}
	s.Player.QueueAdded(ctx, itemID)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": itemID})
}

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil
}

func (s *Server) removeQueue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "Bad ID.")
		return
	}
	ctx := r.Context()
	item, err := s.DB.GetQueueItem(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	if item.ClientID != s.clientID(w, r) {
		if !s.pin.allow() || r.Header.Get("X-Host-Pin") != s.Settings.Get(settings.HostPin) {
			writeErr(w, http.StatusForbidden, "Only the person who added it, or the host, can remove this.")
			return
		}
	}
	if item.Status == "playing" {
		s.Player.TVEnded(ctx, id, false)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if err := s.DB.RemoveQueueItem(ctx, id); err != nil {
		fail(w, err)
		return
	}
	s.Player.QueueRemoved(ctx, id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) moveQueue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	var body struct {
		AfterID int64 `json:"afterId"`
	}
	if !ok || readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if err := s.DB.MoveQueueItem(r.Context(), id, body.AfterID); err != nil {
		fail(w, err)
		return
	}
	s.Player.QueueMoved(r.Context(), id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) playerAction(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = readJSON(r, &body)
	if !s.Player.Command(r.Context(), r.PathValue("action"), body) {
		writeErr(w, http.StatusBadRequest, "Unknown action.")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) getLyrics(w http.ResponseWriter, r *http.Request) {
	song, err := s.DB.GetSong(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if song.LyricsKey == "" {
		writeErr(w, http.StatusNotFound, "No lyrics yet.")
		return
	}
	f, err := s.Media.Open(song.LyricsKey)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "Audio unavailable.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "lyrics.json", time.Time{}, f)
}

func (s *Server) setOffset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		OffsetMs int64 `json:"offsetMs"`
	}
	if readJSON(r, &body) != nil || body.OffsetMs < -30000 || body.OffsetMs > 30000 {
		writeErr(w, http.StatusBadRequest, "Offset must be within 30 seconds.")
		return
	}
	doc, err := s.Jobs.LoadLyrics(r.Context(), id)
	if err != nil || doc == nil {
		writeErr(w, http.StatusNotFound, "No lyrics yet.")
		return
	}
	doc.OffsetMs = body.OffsetMs
	if err := s.Jobs.SaveLyrics(r.Context(), id, doc); err != nil {
		fail(w, err)
		return
	}
	s.Player.Broadcast("song.lyrics", map[string]string{"songId": id})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) setVoices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Voices []lyrics.Voice `json:"voices"`
		Lines  []struct {
			From  int    `json:"from"`
			To    int    `json:"to"`
			Voice string `json:"voice"`
		} `json:"lines"`
	}
	if readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	doc, err := s.Jobs.LoadLyrics(r.Context(), id)
	if err != nil || doc == nil {
		writeErr(w, http.StatusNotFound, "No lyrics yet.")
		return
	}
	if body.Voices != nil {
		for i := range body.Voices {
			body.Voices[i].Source = "host"
		}
		doc.Voices = body.Voices
	}
	for _, l := range body.Lines {
		for i := max(l.From, 0); i <= l.To && i < len(doc.Lines); i++ {
			doc.Lines[i].Voice = l.Voice
		}
	}
	if err := s.Jobs.SaveLyrics(r.Context(), id, doc); err != nil {
		fail(w, err)
		return
	}
	s.Player.Broadcast("song.lyrics", map[string]string{"songId": id})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) setKaraokeLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Karaoke bool `json:"karaoke"`
	}
	if readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	id := r.PathValue("id")
	if err := s.DB.UpdateSong(r.Context(), id, store.SongUpdate{KaraokeVersion: &body.Karaoke}); err != nil {
		fail(w, err)
		return
	}
	if err := s.Jobs.Reprocess(r.Context(), id); err != nil {
		if errors.Is(err, jobs.ErrNeedsFile) {
			writeErr(w, http.StatusBadRequest, "Add the file again to change this.")
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) qr(w http.ResponseWriter, _ *http.Request) {
	png, err := qrcode.Encode(s.Player.JoinURL(), qrcode.Medium, 512)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	isTV := r.URL.Query().Get("client") == "tv"
	if !isTV && !strings.EqualFold(r.URL.Query().Get("room"), s.Settings.Get(settings.RoomCode)) {
		writeErr(w, http.StatusForbidden, "Scan the QR code on the TV to join.")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4096)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	send, detach := s.Player.Attach()
	defer detach()

	snap, err := json.Marshal(player.Event{Type: "snapshot", Data: s.Player.Snapshot(ctx)})
	if err != nil {
		return
	}
	writeCtx, wc := context.WithTimeout(ctx, 5*time.Second)
	err = c.Write(writeCtx, websocket.MessageText, snap)
	wc()
	if err != nil {
		return
	}

	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if isTV {
				s.handleTV(ctx, data)
			}
		}
	}()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-send:
			if !ok {
				return
			}
			wctx, wc := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, raw)
			wc()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pc := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pc()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) handleTV(ctx context.Context, data []byte) {
	var m struct {
		Type   string `json:"type"`
		Ms     int64  `json:"ms"`
		ItemID int64  `json:"itemId"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	switch m.Type {
	case "position":
		s.Player.TVPosition(m.Ms)
	case "ended":
		s.Player.TVEnded(ctx, m.ItemID, true)
	case "error":
		slog.Warn("TV could not play the song", "item", m.ItemID)
		s.Player.TVEnded(ctx, m.ItemID, false)
	}
}

var (
	safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func (s *Server) serveSongFile(w http.ResponseWriter, r *http.Request) {
	id, file := r.PathValue("id"), r.PathValue("file")
	if !safeID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if file == "visual" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, s.Jobs.VisualPath(id))
		return
	}
	song, err := s.DB.GetSong(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	key := ""
	switch file {
	case "audio":
		key = song.AudioKey
	case "guide":
		key = song.GuideKey
	case "video":
		key = song.VideoKey
		if key == "" {
			serveTemp(w, r, filepath.Join(s.Jobs.TempDir(id), "video.mp4"))
			return
		}
	case "quick":
		serveTemp(w, r, filepath.Join(s.Jobs.TempDir(id), "quick.m4a"))
		return
	default:
		http.NotFound(w, r)
		return
	}
	s.serveKey(w, r, key)
}

func serveTemp(w http.ResponseWriter, r *http.Request, p string) {
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, p, st.ModTime(), f)
}

func (s *Server) serveKey(w http.ResponseWriter, r *http.Request, key string) {
	if key == "" {
		http.NotFound(w, r)
		return
	}
	f, err := s.Media.Open(key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		writeErr(w, http.StatusServiceUnavailable, "Audio unavailable.")
		return
	}
	defer f.Close()
	var mod time.Time
	if st, err := s.Media.Stat(key); err == nil {
		mod = st.ModTime
	}
	http.ServeContent(w, r, path.Base(key), mod, f)
}

func (s *Server) serveCover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, s.Jobs.CoverPath(id))
}

func (s *Server) serveCoverPalette(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !safeID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, s.Jobs.PalettePath(id))
}

// webRoutes serves the embedded frontend.
func (s *Server) webRoutes(mux *http.ServeMux) {
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			s.serveWebFile(w, r, name)
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/tv", http.StatusFound) })
	mux.HandleFunc("GET /tv", page("tv.html"))
	remote := page("remote.html")
	mux.HandleFunc("GET /r/{room}", func(w http.ResponseWriter, r *http.Request) {
		// Set the guest's cookie now, so the page's first requests all share it.
		s.clientID(w, r)
		remote(w, r)
	})
	mux.HandleFunc("GET /admin", page("admin.html"))
	mux.HandleFunc("GET /admin/{rest...}", page("admin.html"))
	mux.HandleFunc("GET /assets/{file...}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		s.serveWebFile(w, r, "assets/"+r.PathValue("file"))
	})
}

// serveWebFile prefers a precompressed copy when the browser accepts it.
func (s *Server) serveWebFile(w http.ResponseWriter, r *http.Request, name string) {
	ctype := mime.TypeByExtension(path.Ext(name))
	accept := r.Header.Get("Accept-Encoding")
	for _, enc := range []struct{ tok, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !strings.Contains(accept, enc.tok) {
			continue
		}
		if b, err := fs.ReadFile(s.Web, name+enc.ext); err == nil {
			w.Header().Set("Content-Encoding", enc.tok)
			w.Header().Set("Content-Type", ctype)
			w.Header().Add("Vary", "Accept-Encoding")
			_, _ = w.Write(b)
			return
		}
	}
	b, err := fs.ReadFile(s.Web, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Add("Vary", "Accept-Encoding")
	_, _ = w.Write(b)
}

func (s *Server) clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if proxy := s.Settings.Get(settings.TrustedProxy); proxy != "" && ip != nil && ip.Equal(net.ParseIP(proxy)) {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if real := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); real != nil {
			return real
		}
	}
	return ip
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	proxy := s.Settings.Get(settings.TrustedProxy)
	return proxy != "" && host == proxy && r.Header.Get("X-Forwarded-Proto") == "https"
}
