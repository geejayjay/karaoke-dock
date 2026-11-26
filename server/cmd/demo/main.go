// Command demo fills a separate data folder with a small library of made-up
// songs and a queue, for screenshots and trying the app. It downloads nothing:
// covers are optional photos from -covers (see covers.go), songs have a second of
// quiet audio, and the song on the TV has quiet audio and lyrics written for it.
//
//	go -C server run ./cmd/demo -data ../data-demo
//	bin/karaokedock -metadata data-demo/metadata
//
// Both run from the project folder.
//
// Admin's password in the demo is "demo-password".
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"karaokedock/internal/admin"
	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/settings"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
	"karaokedock/internal/visual"
)

type song struct {
	title, artist, album, genre string
	year                        int
	sec                         int64
	plays                       int
	state                       string // empty means ready
	cover                       string // file in covers/, see covers.go
}

// Made-up songs, each named after its cover photo.
var library = []song{
	{"Blue Stage Lights", "The Front Row", "Encore", "Rock", 2024, 236, 21, "", "blue-stage-lights"},
	{"Silver Microphone", "Velvet Static", "Open Mic Night", "Jazz", 2023, 198, 34, "", "silver-microphone"},
	{"Ocean at Sundown", "Tidewater", "Golden Coast", "Indie", 2025, 251, 17, "", "ocean-at-sundown"},
	{"Spin It Slow", "Monochrome Club", "Side B", "Soul", 2022, 223, 26, "", "spin-it-slow"},
	{"Stars Over the Summit", "Northern Arc", "High Altitude", "Folk", 2025, 267, 12, "separating", "stars-over-the-summit"},
}

// The song on the TV in the demo is made up, with lyrics written for it.
var nowSinging = song{"Neon Skyline", "The Dock Street Band", "Late Night Sessions", "Pop", 2025, 214, 0, "", "neon-skyline"}

var nowLyrics = []string{
	"City lights are calling out my name",
	"Every window glowing like a flame",
	"Grab the mic and sing it loud tonight",
	"We are dancing under neon light",
	"Oh oh, the night is young",
	"Oh oh, our song has just begun",
	"Hold on, the skyline sings along",
	"Hold on, we're right where we belong",
}

// Who's in the queue, after the song on screen.
var queue = []struct{ title, singer string }{
	{"Blue Stage Lights", "Mia"},
	{"Silver Microphone", "Jordan"},
	{"Ocean at Sundown", "Sam & Alex"},
	{"Spin It Slow", "Priya"},
}

func main() {
	dir := flag.String("data", "data-demo", "folder to fill; it must not already hold a library")
	covers := flag.String("covers", "cmd/demo/covers", "folder of optional cover photos, named after each song")
	borrow := flag.String("visual", "", "optional visual.json of a real song, for a lifelike TV background and song bar")
	flag.Parse()
	if err := run(context.Background(), *dir, *covers, *borrow); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir, coverDir, borrow string) error {
	// A real song's analysis makes the TV move like a real song. The demo song
	// takes its length, so they line up.
	var borrowed []byte
	if borrow != "" {
		raw, err := os.ReadFile(borrow)
		if err != nil {
			return err
		}
		var v struct {
			Rate  float64 `json:"rate"`
			Level []byte  `json:"level"`
		}
		if err := json.Unmarshal(raw, &v); err != nil || v.Rate <= 0 {
			return fmt.Errorf("%s isn't a song analysis", borrow)
		}
		borrowed = raw
		nowSinging.sec = int64(float64(len(v.Level)) / v.Rate)
	}
	meta := filepath.Join(dir, "metadata")
	if _, err := os.Stat(filepath.Join(meta, "karaokedock.db")); err == nil {
		return fmt.Errorf("%s already has a library; delete the folder to start over", dir)
	}
	if err := os.MkdirAll(meta, 0o755); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(meta, "karaokedock.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	abs, _ := filepath.Abs(dir)
	cfg, err := settings.Load(ctx, db, map[string]string{
		settings.MediaRoot: filepath.Join(abs, "media"),
		settings.TempRoot:  filepath.Join(abs, "tmp"),
	})
	if err != nil {
		return err
	}
	// Nothing to prepare, and the demo stays as it is.
	// An example network address keeps screenshots free of the real one.
	if err := cfg.SetRaw(ctx, map[string]string{settings.PauseImports: "true", settings.RoomCode: "KARA42", settings.LanIP: "192.168.1.20"}); err != nil {
		return err
	}
	if err := admin.New(db, cfg, meta).Setup(ctx, "demo-password"); err != nil {
		return err
	}
	fs := media.NewFs(cfg.Get(settings.MediaRoot))

	// Ready songs need an audio file to count as ready; a second of quiet will do.
	blip, err := testAudio(1)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	covers := map[string]string{"demo0000000": nowSinging.cover}
	for i, s := range library {
		id := fmt.Sprintf("demo%07d", i+1)
		ids[s.title] = id
		covers[id] = s.cover
		if err := addSong(ctx, db, id, s); err != nil {
			return err
		}
		if s.state == "" {
			key := media.AudioKey(id, ".m4a")
			if _, err := fs.Write(key, bytes.NewReader(blip)); err != nil {
				return err
			}
			if err := db.UpdateSong(ctx, id, store.SongUpdate{AudioKey: &key}); err != nil {
				return err
			}
			// Real lyrics aren't ours to share, so the demo copies say so.
			if err := saveLyrics(ctx, db, fs, id, []string{"This is a demo song with no real lyrics"}, 12000); err != nil {
				return err
			}
		}
	}
	nowID := "demo0000000"
	if err := addSong(ctx, db, nowID, nowSinging); err != nil {
		return err
	}
	if err := addPlayable(ctx, db, fs, nowID, nowSinging.sec); err != nil {
		return err
	}
	if err := analyse(ctx, fs, meta, nowID, borrowed); err != nil {
		return err
	}

	if err := addCovers(ctx, db, meta, coverDir, covers); err != nil {
		return err
	}

	if _, err := db.AddToQueue(ctx, nowID, "Chris", "demo-client-chris"); err != nil {
		return err
	}
	for i, q := range queue {
		if _, err := db.AddToQueue(ctx, ids[q.title], q.singer, fmt.Sprintf("demo-client-%d", i)); err != nil {
			return err
		}
	}
	fmt.Printf("Demo ready in %s: %d songs, %d in the queue. Room code KARA42.\n", dir, len(library)+1, len(queue)+1)
	return nil
}

func addSong(ctx context.Context, db *store.Store, id string, s song) error {
	if _, err := db.InsertSong(ctx, id, s.title); err != nil {
		return err
	}
	var artists []store.ArtistInput
	for _, name := range strings.Split(s.artist, ", ") {
		artists = append(artists, store.ArtistInput{Name: name})
	}
	if err := db.SetSongDetails(ctx, id, artists, &store.AlbumInput{Title: s.album, Year: s.year}); err != nil {
		return err
	}
	state := s.state
	if state == "" {
		state = "complete"
	}
	ms := s.sec * 1000
	if err := db.UpdateSong(ctx, id, store.SongUpdate{State: &state, Genre: &s.genre, DurationMs: &ms}); err != nil {
		return err
	}
	for range s.plays {
		if err := db.IncrementPlayCount(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// testAudio makes a quiet tone of the given length with FFmpeg.
func testAudio(sec int64) ([]byte, error) {
	tmp, err := os.CreateTemp("", "demo-*.m4a")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=44100",
		"-t", fmt.Sprint(sec), "-af", "volume=0.02", "-c:a", "aac", tmp.Name()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, out)
	}
	return os.ReadFile(tmp.Name())
}

// A short, quiet made-up track: a kick on every beat, hi-hats between, a chord
// that changes each bar, and sections that take turns being soft and loud. The
// app analyses it like any song, so the TV's background and song bar move.
const demoMusic = "0.16*(0.55+0.45*mod(floor(t/16),2))*(" +
	"0.9*sin(2*PI*55*t)*exp(-12*mod(t,0.5))" + // kick
	"+0.35*(2*random(0)-1)*exp(-35*mod(t+0.25,0.5))" + // hi-hat
	"+0.12*(sin(2*PI*220*t*(1+0.25*mod(floor(t/2),2)))+sin(2*PI*277.18*t)+sin(2*PI*329.63*t)))" // chord

// musicAudio renders demoMusic with FFmpeg.
func musicAudio(sec int64) ([]byte, error) {
	tmp, err := os.CreateTemp("", "demo-*.m4a")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	src := fmt.Sprintf("aevalsrc=exprs='%s':s=44100:d=%d", demoMusic, sec)
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", src, "-c:a", "aac", tmp.Name()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, out)
	}
	return os.ReadFile(tmp.Name())
}

// addPlayable gives a song quiet made-up music and word-timed lyrics.
func addPlayable(ctx context.Context, db *store.Store, fs *media.Fs, id string, sec int64) error {
	audio, err := musicAudio(sec)
	if err != nil {
		return err
	}
	audioKey := media.AudioKey(id, ".m4a")
	if _, err := fs.Write(audioKey, bytes.NewReader(audio)); err != nil {
		return err
	}
	if err := db.UpdateSong(ctx, id, store.SongUpdate{AudioKey: &audioKey}); err != nil {
		return err
	}

	return saveLyrics(ctx, db, fs, id, nowLyrics, sec*1000)
}

// saveLyrics times lines evenly, repeating them until the song ends, and saves
// them as the song's lyrics.
func saveLyrics(ctx context.Context, db *store.Store, fs *media.Fs, id string, text []string, untilMs int64) error {
	// Lines start after a short intro and repeat until the song ends.
	doc := &lyrics.Doc{Version: 1, Source: "demo", Timing: "word", Voices: []lyrics.Voice{}}
	at := int64(8000)
	for i := 0; at+4000 <= untilMs; i++ {
		text := text[i%len(text)]
		words := strings.Fields(text)
		step := int64(3600) / int64(len(words))
		line := lyrics.Line{StartMs: at}
		for j, w := range words {
			start := at + int64(j)*step
			line.Words = append(line.Words, lyrics.Word{Text: w, StartMs: start, EndMs: start + step})
		}
		line.EndMs = at + 3600
		doc.Lines = append(doc.Lines, line)
		at += 4200
		if i%4 == 3 {
			at += 2500 // a breath between verses
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	lyricsKey := media.LyricsKey(id)
	if _, err := fs.Write(lyricsKey, bytes.NewReader(raw)); err != nil {
		return err
	}
	if err := db.SetLyricsText(ctx, id, doc.PlainText()); err != nil {
		return err
	}
	// Line counts are left out: they describe lyrics read from a karaoke video,
	// and these weren't.
	has := true
	return db.UpdateSong(ctx, id, store.SongUpdate{LyricsKey: &lyricsKey, HasLyrics: &has})
}

// analyse makes the sound analysis the TV's background and song bar follow, as
// the app does when it prepares a song. The demo keeps processing paused, so
// the app wouldn't do it here.
// When a real song's analysis is given, it's used instead.
func analyse(ctx context.Context, fs *media.Fs, meta, id string, raw []byte) error {
	if raw == nil {
		path, err := fs.Path(media.AudioKey(id, ".m4a"))
		if err != nil {
			return err
		}
		samples, err := tools.MonoSamples(ctx, path, visual.SampleRate)
		if err != nil {
			return err
		}
		if raw, err = json.Marshal(visual.Analyze(samples)); err != nil {
			return err
		}
	}
	dir := filepath.Join(meta, "songs", id) // where the app keeps visual.json
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "visual.json"), raw, 0o644)
}
