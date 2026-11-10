// Package player holds the live queue and playback state and pushes it to
// every connected screen.
package player

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"karaokedock/internal/jobs"
	"karaokedock/internal/media"
	"karaokedock/internal/settings"
	"karaokedock/internal/store"
)

// UpNextSize caps the queue carried in snapshots.
const UpNextSize = 10

// Source says what the TV should play.
type Source struct {
	Kind       string `json:"kind"` // "audio" or "video"
	AudioURL   string `json:"audioUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	VideoMuted bool   `json:"videoMuted,omitempty"`
	QuickMode  bool   `json:"quickMode,omitempty"`
	// Silence cut from this audio. Lyrics are timed to the uncut song.
	TrimStartMs int64 `json:"trimStartMs,omitempty"`
	TrimEndMs   int64 `json:"trimEndMs,omitempty"`
}

func (s Source) playedLength(fullMs int64) int64 { return fullMs - s.TrimStartMs - s.TrimEndMs }

// CurrentSong is the song on screen.
type CurrentSong struct {
	ItemID      int64  `json:"itemId"`
	Singer      string `json:"singer"`
	Owner       string `json:"owner,omitempty"` // store.ClientTag of who added it
	SongID      string `json:"songId"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	DurationMs  int64  `json:"durationMs"`
	State       string `json:"state"`
	AccentColor string `json:"accentColor"`
	CoverID     string `json:"coverId,omitempty"`
	HasBg       bool   `json:"hasBg"`
	HasLyrics   bool   `json:"hasLyrics"`
	HasGuide    bool   `json:"hasGuide"`
	Karaoke     bool   `json:"karaoke"`
	Source      Source `json:"source"`
}

// State is the live player.
type State struct {
	Current    *CurrentSong `json:"current"`
	Paused     bool         `json:"paused"`
	PositionMs int64        `json:"positionMs"`
	SeekSeq    int64        `json:"seekSeq"`
	SeekMs     int64        `json:"seekMs"`
	AudioMode  string       `json:"audioMode"`
}

// Network is the server's connection status.
type Network struct {
	Online  bool `json:"online"`
	MediaOK bool `json:"mediaOk"`
}

// Event is one message to the screens.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type client struct {
	send chan []byte
}

// Player is the live hub.
type Player struct {
	db    *store.Store
	cfg   *settings.Manager
	jobs  *jobs.Manager
	media *media.Fs
	port  int

	mu      sync.Mutex
	state   State
	online  bool
	clients map[*client]struct{}
}

// New creates the player.
func New(db *store.Store, cfg *settings.Manager, j *jobs.Manager, m *media.Fs, port int) *Player {
	return &Player{db: db, cfg: cfg, jobs: j, media: m, port: port, clients: map[*client]struct{}{}}
}

// Resume restores a song that was playing before a restart, and starts checking
// the network.
func (p *Player) Resume(ctx context.Context) {
	p.mu.Lock()
	item, err := p.db.CurrentItem(ctx)
	if err == nil && item != nil {
		p.startLocked(ctx, item)
	}
	p.mu.Unlock()
	if err == nil && item == nil {
		p.Advance(ctx, false)
	}
	go p.watchNetwork(ctx)
}

func checkOnline() bool {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (p *Player) watchNetwork(ctx context.Context) {
	t := time.NewTicker(45 * time.Second)
	defer t.Stop()
	for {
		on := checkOnline()
		p.mu.Lock()
		changed := on != p.online
		p.online = on
		p.mu.Unlock()
		if changed {
			p.BroadcastNetwork()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Online reports the last internet check.
func (p *Player) Online() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.online
}

// BroadcastNetwork pushes connection status.
func (p *Player) BroadcastNetwork() {
	p.Broadcast("network.status", p.NetworkStatus())
}

// NetworkStatus returns the current status.
func (p *Player) NetworkStatus() Network {
	return Network{Online: p.Online(), MediaOK: p.media.Healthy()}
}

// LanIP finds the address phones should use.
func (p *Player) LanIP() string {
	if ip := p.cfg.Get(settings.LanIP); ip != "" {
		return ip
	}
	var routed net.IP
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil { // sends nothing
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() {
			routed = a.IP
		}
	}
	return pickLanIP(routed, interfaceIPs())
}

func interfaceIPs() []net.IP {
	addrs, _ := net.InterfaceAddrs()
	var ips []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// pickLanIP prefers the routed address when it's a home-network address. VPNs
// can own the default route with an address phones can't reach, so otherwise it
// looks for a private address, with 192.168 first and virtual-adapter 172 last.
func pickLanIP(routed net.IP, ips []net.IP) string {
	if routed != nil && routed.IsPrivate() {
		return routed.String()
	}
	for _, prefix := range []byte{192, 10, 172} {
		for _, ip := range ips {
			if ip.IsPrivate() && ip.To4()[0] == prefix {
				return ip.String()
			}
		}
	}
	if routed != nil {
		return routed.String()
	}
	return "127.0.0.1"
}

// JoinURL is the address in the QR code.
func (p *Player) JoinURL() string {
	room := p.cfg.Get(settings.RoomCode)
	domain := p.cfg.Get(settings.PublicURL)
	mode := p.cfg.Get(settings.QRMode)
	if domain != "" && (mode == "domain" || (mode == "auto" && p.Online())) {
		for len(domain) > 0 && domain[len(domain)-1] == '/' {
			domain = domain[:len(domain)-1]
		}
		return domain + "/r/" + room
	}
	host := p.LanIP()
	if p.port != 80 {
		host += ":" + strconv.Itoa(p.port)
	}
	return "http://" + host + "/r/" + room
}

// CanPlay reports whether a song in this state can start.
func (p *Player) CanPlay(state string) bool {
	switch state {
	case jobs.StatePlayable, jobs.StateSeparating:
		return true
	case jobs.StateKaraokeReady, jobs.StateAligning, jobs.StateComplete:
		return p.media.Healthy()
	}
	return false
}

func mediaURL(id, file string) string { return "/media/" + id + "/" + file }

func (p *Player) source(s *store.Song, mode string) (Source, string) {
	id := s.ID
	if s.AudioKey != "" && p.CanPlay(s.State) && s.State != jobs.StatePlayable && s.State != jobs.StateSeparating {
		if mode == "guide" && s.GuideKey != "" {
			return Source{Kind: "audio", AudioURL: mediaURL(id, "guide"), TrimStartMs: s.TrimStartMs, TrimEndMs: s.TrimEndMs}, "guide"
		}
		return Source{Kind: "audio", AudioURL: mediaURL(id, "audio"), TrimStartMs: s.TrimStartMs, TrimEndMs: s.TrimEndMs}, "instrumental"
	}
	if mode == "" {
		mode = p.cfg.Get(settings.UnprocessedAudio)
	}
	if mode == "original" {
		return Source{Kind: "video", VideoURL: mediaURL(id, "video")}, "original"
	}
	return Source{Kind: "video", VideoURL: mediaURL(id, "video"), AudioURL: mediaURL(id, "quick"), VideoMuted: true, QuickMode: true}, "quick"
}

func (p *Player) startLocked(ctx context.Context, item *store.QueueItem) {
	song, err := p.db.GetSong(ctx, item.Song.ID)
	if err != nil {
		slog.Error("start song", "err", err)
		return
	}
	mode := ""
	if p.cfg.Get(settings.DefaultVocalMode) == "guide" {
		mode = "guide"
	}
	src, audioMode := p.source(song, mode)
	p.state = State{Current: p.currentFrom(item, song, src), AudioMode: audioMode, SeekSeq: p.state.SeekSeq}
}

func (p *Player) currentFrom(item *store.QueueItem, s *store.Song, src Source) *CurrentSong {
	return &CurrentSong{ItemID: item.ID, Singer: item.Singer, Owner: store.ClientTag(item.ClientID), SongID: s.ID, Title: s.Title, Artist: s.Artist, DurationMs: src.playedLength(s.DurationMs),
		State: s.State, AccentColor: s.AccentColor, CoverID: s.CoverID(), HasBg: s.HasBg, HasLyrics: s.HasLyrics,
		HasGuide: s.GuideKey != "", Karaoke: s.KaraokeVersion, Source: src}
}

// Advance leaves the current song and starts the next playable one.
func (p *Player) Advance(ctx context.Context, played bool) {
	p.mu.Lock()
	var endedSong string
	var wasItem int64
	if c := p.state.Current; c != nil {
		endedSong, wasItem = c.SongID, c.ItemID
		_ = p.db.SetQueueStatus(ctx, c.ItemID, "done")
		if played {
			_ = p.db.IncrementPlayCount(ctx, c.SongID)
		}
	}
	next, err := p.db.NextPlayable(ctx, p.CanPlay)
	if err != nil {
		slog.Error("next song", "err", err)
	}
	if next != nil {
		_ = p.db.SetQueueStatus(ctx, next.ID, "playing")
		p.startLocked(ctx, next)
	} else {
		p.state = State{SeekSeq: p.state.SeekSeq}
	}
	state := p.state
	p.mu.Unlock()

	if endedSong != "" {
		p.jobs.SongEnded(ctx, endedSong)
	}
	p.Broadcast("player.state", state)
	data := map[string]any{"op": "start", "upNext": p.upNext(ctx)}
	if wasItem != 0 {
		data["endedId"] = wasItem
	}
	if next != nil {
		data["startedId"] = next.ID
	}
	p.Broadcast("queue.updated", data)
}

func (p *Player) upNext(ctx context.Context) []store.QueueItem {
	page, err := p.db.ListQueue(ctx, "", UpNextSize)
	if err != nil {
		slog.Error("up next", "err", err)
		return []store.QueueItem{}
	}
	return page.Items
}

// State returns a copy of the live state.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Snapshot is sent to every screen on connect.
func (p *Player) Snapshot(ctx context.Context) map[string]any {
	return map[string]any{
		"room":     p.cfg.Get(settings.RoomCode),
		"joinUrl":  p.JoinURL(),
		"player":   p.State(),
		"upNext":   p.upNext(ctx),
		"network":  p.NetworkStatus(),
		"playback": PlaybackSettings(p.cfg),
	}
}

// PlaybackSettings are the settings screens need.
func PlaybackSettings(c *settings.Manager) map[string]any {
	return map[string]any{
		"animationLevel":   c.Get(settings.AnimationLevel),
		"countdown":        c.Bool(settings.Countdown),
		"countdownSeconds": c.Int(settings.CountdownSeconds),
	}
}

// QueueAdded announces a new item, and starts it if nothing is playing.
func (p *Player) QueueAdded(ctx context.Context, itemID int64) {
	item, err := p.db.GetQueueItem(ctx, itemID)
	if err != nil {
		return
	}
	p.Broadcast("queue.updated", map[string]any{"op": "add", "item": item, "upNext": p.upNext(ctx)})
	p.startIfIdle(ctx)
}

// QueueRemoved announces a deleted item.
func (p *Player) QueueRemoved(ctx context.Context, itemID int64) {
	p.Broadcast("queue.updated", map[string]any{"op": "remove", "id": itemID, "upNext": p.upNext(ctx)})
}

// SingerChanged announces a new name on a queued or playing song.
func (p *Player) SingerChanged(ctx context.Context, itemID int64, singer string) {
	p.mu.Lock()
	var st *State
	if c := p.state.Current; c != nil && c.ItemID == itemID {
		c.Singer = singer
		copied := p.state
		st = &copied
	}
	p.mu.Unlock()
	if st != nil {
		p.Broadcast("player.state", *st)
	}
	p.QueueMoved(ctx, itemID)
}

// QueueMoved announces a reorder.
func (p *Player) QueueMoved(ctx context.Context, itemID int64) {
	p.Broadcast("queue.updated", map[string]any{"op": "move", "id": itemID, "upNext": p.upNext(ctx)})
}

func (p *Player) startIfIdle(ctx context.Context) {
	p.mu.Lock()
	idle := p.state.Current == nil
	p.mu.Unlock()
	if idle {
		p.Advance(ctx, false)
	}
}

// SongChanged implements jobs.Notifier.
func (p *Player) SongChanged(id string) {
	ctx := context.Background()
	song, err := p.db.GetSong(ctx, id)
	if err != nil {
		return
	}
	row := song.Row()
	p.mu.Lock()
	var cur *State
	if c := p.state.Current; c != nil && c.SongID == id {
		// Keep the source: audio changes only when the host switches.
		c.State, c.HasLyrics, c.HasGuide = song.State, song.HasLyrics, song.GuideKey != ""
		c.HasBg, c.AccentColor, c.Title, c.Artist = song.HasBg, song.AccentColor, song.Title, song.Artist
		c.CoverID = song.CoverID()
		st := p.state
		cur = &st
	}
	p.mu.Unlock()
	p.Broadcast("song.state", map[string]any{"song": row, "error": song.Error})
	if song.State == jobs.StateFailed {
		p.Broadcast("song.failed", map[string]any{"songId": id, "error": song.Error})
	}
	if cur != nil {
		p.Broadcast("player.state", *cur)
	}
	p.startIfIdle(ctx)
}

// SongProgress implements jobs.Notifier.
func (p *Player) SongProgress(id, stage string, percent float64) {
	p.Broadcast("song.progress", map[string]any{"songId": id, "stage": stage, "percent": percent})
}

// Command runs a host action. It returns false for an unknown action.
func (p *Player) Command(ctx context.Context, action string, body map[string]any) bool {
	switch action {
	case "play", "pause":
		p.mu.Lock()
		p.state.Paused = action == "pause"
		st := p.state
		p.mu.Unlock()
		p.Broadcast("player.state", st)
	case "skip":
		p.Advance(ctx, false)
	case "seek":
		ms, _ := body["ms"].(float64)
		p.mu.Lock()
		p.state.SeekSeq++
		p.state.SeekMs = max(int64(ms), 0)
		st := p.state
		p.mu.Unlock()
		p.Broadcast("player.state", st)
	case "vocal-mode":
		mode, _ := body["mode"].(string)
		p.setAudioMode(ctx, mode)
	default:
		return false
	}
	return true
}

func (p *Player) setAudioMode(ctx context.Context, mode string) {
	p.mu.Lock()
	cur := p.state.Current
	if cur == nil {
		p.mu.Unlock()
		return
	}
	song, err := p.db.GetSong(ctx, cur.SongID)
	if err != nil {
		p.mu.Unlock()
		return
	}
	src, applied := p.source(song, mode)
	cur.Source = src
	cur.DurationMs = src.playedLength(song.DurationMs)
	p.state.AudioMode = applied
	st := p.state
	p.mu.Unlock()
	p.Broadcast("player.state", st)
}

// TVPosition records the TV's clock.
func (p *Player) TVPosition(ms int64) {
	p.mu.Lock()
	p.state.PositionMs = ms
	p.mu.Unlock()
	p.Broadcast("player.position", map[string]any{"ms": ms})
}

// TVEnded moves on when the TV finishes the current item. It's also how a
// playing song is removed from the queue.
func (p *Player) TVEnded(ctx context.Context, itemID int64, played bool) {
	p.mu.Lock()
	match := p.state.Current != nil && p.state.Current.ItemID == itemID
	p.mu.Unlock()
	if match {
		p.Advance(ctx, played)
	}
}

// Attach adds a connected screen.
func (p *Player) Attach() (<-chan []byte, func()) {
	c := &client{send: make(chan []byte, 64)}
	p.mu.Lock()
	p.clients[c] = struct{}{}
	p.mu.Unlock()
	return c.send, func() {
		p.mu.Lock()
		if _, ok := p.clients[c]; ok {
			delete(p.clients, c)
			close(c.send)
		}
		p.mu.Unlock()
	}
}

// Broadcast sends an event to every screen, dropping any that can't keep up.
// A dropped screen reconnects and gets a new snapshot.
func (p *Player) Broadcast(kind string, data any) {
	raw, err := json.Marshal(Event{kind, data})
	if err != nil {
		slog.Error("broadcast", "err", err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.clients {
		select {
		case c.send <- raw:
		default:
			delete(p.clients, c)
			close(c.send)
		}
	}
}
