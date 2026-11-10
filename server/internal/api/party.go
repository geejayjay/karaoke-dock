package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
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

var iconSizes = map[int]bool{180: true, 192: true, 512: true}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	size, err := strconv.Atoi(r.PathValue("size"))
	if err != nil || !iconSizes[size] {
		http.NotFound(w, r)
		return
	}
	var out bytes.Buffer
	if err := png.Encode(&out, halftoneIcon(size)); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(out.Bytes())
}

// halftoneIcon draws a circle of dots that shrink towards its edge.
func halftoneIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	dark := color.RGBA{13, 11, 20, 255}
	for y := range size {
		for x := range size {
			img.SetRGBA(x, y, dark)
		}
	}
	center := float64(size) / 2
	cell := float64(size) / 16
	reach := float64(size) * 0.42
	for row := -9.0; row <= 9; row++ {
		offset := 0.0
		if int(row)%2 != 0 {
			offset = cell / 2
		}
		for col := -9.0; col <= 9; col++ {
			cx, cy := center+col*cell+offset, center+row*cell*0.87
			dist := math.Hypot(cx-center, cy-center)
			if dist >= reach {
				continue
			}
			radius := cell * 0.48 * (1 - dist/reach*0.75)
			mix := (cx + cy) / float64(2*size)
			c := color.RGBA{uint8(255 - 112*mix), uint8(77 + 30*mix), uint8(141 + 114*mix), 255}
			for py := int(cy - radius); py <= int(cy+radius)+1; py++ {
				for px := int(cx - radius); px <= int(cx+radius)+1; px++ {
					if math.Hypot(float64(px)-cx, float64(py)-cy) <= radius && px >= 0 && py >= 0 && px < size && py < size {
						img.SetRGBA(px, py, c)
					}
				}
			}
		}
	}
	return img
}
