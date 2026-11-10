package api

import (
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"karaokedock/internal/settings"
	"karaokedock/internal/store"
)

// partyRoutes are the extras guests and hosts use during a party.
func (s *Server) partyRoutes(mux *http.ServeMux, room, host func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /api/queue/{id}/partner", room(s.setPartner))
	mux.HandleFunc("DELETE /api/queue", host(s.clearQueue))
	mux.HandleFunc("POST /api/react", room(s.react))
	mux.HandleFunc("GET /r/{room}/manifest.webmanifest", s.manifest)
	mux.HandleFunc("GET /icon/{size}", s.icon)
	mux.HandleFunc("GET /icon.svg", s.iconSVG)
	mux.HandleFunc("GET /fonts/script", s.scriptFont)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/icon/192", http.StatusMovedPermanently)
	})
}

// canEditItem allows the person who queued a song, or the host.
func (s *Server) canEditItem(w http.ResponseWriter, r *http.Request, item *store.QueueItem) bool {
	if item.ClientID == s.clientID(w, r) {
		return true
	}
	if s.pin.allow() && r.Header.Get("X-Host-Pin") == s.Settings.Get(settings.HostPin) {
		return true
	}
	writeErr(w, http.StatusForbidden, "Only the person who added it, or the host, can change this.")
	return false
}

const maxPartnerName = 40

// setPartner adds or removes a second singer on a queued song.
func (s *Server) setPartner(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	var body struct {
		Partner string `json:"partner"`
	}
	if !ok || readJSON(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	partner := strings.TrimSpace(body.Partner)
	if len(partner) > maxPartnerName {
		writeErr(w, http.StatusBadRequest, "Enter a name up to 40 letters.")
		return
	}
	ctx := r.Context()
	item, err := s.DB.GetQueueItem(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	if !s.canEditItem(w, r, item) {
		return
	}
	singer, _, _ := strings.Cut(item.Singer, " & ")
	if partner != "" {
		singer += " & " + partner
	}
	if err := s.DB.SetSinger(ctx, id, singer); err != nil {
		fail(w, err)
		return
	}
	s.Player.SingerChanged(ctx, id, singer)
	writeJSON(w, 200, map[string]string{"singer": singer})
}

func (s *Server) clearQueue(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.ClearQueue(r.Context()); err != nil {
		fail(w, err)
		return
	}
	s.Player.QueueMoved(r.Context(), 0)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var reactions = map[string]bool{"👏": true, "🔥": true, "❤️": true, "🎉": true, "😂": true, "🎤": true}

const reactionGap = 200 * time.Millisecond

// react sends an emoji to float up the TV, a few per second at most per guest.
func (s *Server) react(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Emoji string `json:"emoji"`
	}
	if readJSON(r, &body) != nil || !reactions[body.Emoji] {
		writeErr(w, http.StatusBadRequest, "Bad request.")
		return
	}
	client := s.clientID(w, r)
	now := time.Now()
	s.mu.Lock()
	if s.reacted == nil {
		s.reacted = map[string]time.Time{}
	}
	tooSoon := now.Sub(s.reacted[client]) < reactionGap
	if !tooSoon {
		s.reacted[client] = now
	}
	s.mu.Unlock()
	if !tooSoon {
		s.Player.Broadcast("reaction", map[string]string{"emoji": body.Emoji})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// manifest lets a phone add this party to its home screen.
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	room := r.PathValue("room")
	if !strings.EqualFold(room, s.Settings.Get(settings.RoomCode)) {
		http.NotFound(w, r)
		return
	}
	icon := func(size string) map[string]string {
		return map[string]string{"src": "/icon/" + size, "sizes": size + "x" + size, "type": "image/png"}
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": "KaraokeDock", "short_name": "KaraokeDock", "start_url": "/r/" + room, "scope": "/r/" + room,
		"display": "standalone", "background_color": "#0d0b14", "theme_color": "#0d0b14",
		"icons": []map[string]string{icon("192"), icon("512")},
	})
}

// iconFiles are the logo, drawn by assets/make_logo.py.
//
//go:embed icons
var iconFiles embed.FS

var iconSizes = map[string]bool{"180": true, "192": true, "512": true}

// icon serves the logo as a PNG of a given size, for home screens.
func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	size := r.PathValue("size")
	if !iconSizes[size] {
		http.NotFound(w, r)
		return
	}
	serveIcon(w, r, "icons/icon-"+size+".png", "image/png")
}

// iconSVG serves the logo as the browser tab icon.
func (s *Server) iconSVG(w http.ResponseWriter, r *http.Request) {
	serveIcon(w, r, "icons/icon.svg", "image/svg+xml")
}

func serveIcon(w http.ResponseWriter, r *http.Request, name, contentType string) {
	raw, err := iconFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(raw)
}

// scriptFont serves an optional font for the "dock" in the name, from
// <metadata>/fonts/script.otf. It isn't part of the app, so a font that can't be
// shared stays on this machine. Without it, pages use the built-in script font.
func (s *Server) scriptFont(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.MetadataDir, "fonts", "script.otf")
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/otf")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}
