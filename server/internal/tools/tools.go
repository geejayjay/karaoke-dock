// Package tools wraps yt-dlp and FFmpeg.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg" // decode and encode thumbnails
	_ "image/png"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// VideoID accepts only an 11-character YouTube ID.
var VideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

var urlID = regexp.MustCompile(`(?:youtu\.be/|[?&]v=|/shorts/|/embed/|/live/)([A-Za-z0-9_-]{11})(?:[?&#/]|$)`)

// ExtractVideoID finds an ID in pasted text, or returns "".
func ExtractVideoID(s string) string {
	s = strings.TrimSpace(s)
	if VideoID.MatchString(s) && !strings.ContainsAny(s, " ") && strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' || r == '_' || r == '-' }) >= 0 {
		return s
	}
	if strings.Contains(s, "youtu") {
		if m := urlID.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

func command(ctx context.Context, low bool, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	applyProcAttrs(cmd, low)
	return cmd
}

func run(ctx context.Context, low bool, name string, args ...string) error {
	cmd := command(ctx, low, name, args...)
	var tail bytes.Buffer
	cmd.Stderr = &tail
	if err := startAndLower(cmd, low); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, lastBytes(tail.Bytes(), 600))
	}
	return nil
}

func lastBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

// Info is the part of yt-dlp's metadata the app uses.
type Info struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Track     string             `json:"track"`
	Artist    string             `json:"artist"`
	Album     string             `json:"album"`
	Uploader  string             `json:"uploader"`
	Channel   string             `json:"channel"`
	Duration  float64            `json:"duration"`
	Language  string             `json:"language"`
	Subtitles map[string]any     `json:"subtitles"`
	Extra     map[string]float64 `json:"-"`
}

// Download fetches a song into dir as source.mp4 with info.json and jpg.
func Download(ctx context.Context, id, dir string, height int, progress func(float64)) (*Info, error) {
	if !VideoID.MatchString(id) {
		return nil, errors.New("bad video id")
	}
	format := fmt.Sprintf("bv*[vcodec^=avc1][height<=%d]+ba[ext=m4a]/bv*[vcodec^=avc1][height<=%d]+ba/b[vcodec^=avc1][height<=%d]/bv*[height<=%d]+ba/b[height<=%d]/b", height, height, height, height, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cmd := command(ctx, false, "yt-dlp",
		"--no-playlist", "--no-warnings", "--newline", "--ignore-config",
		"-f", format, "--merge-output-format", "mp4",
		"--write-info-json",
		"--progress-template", "download:KD%(progress._percent_str)s",
		"-o", filepath.Join(dir, "source.%(ext)s"),
		"--", "https://www.youtube.com/watch?v="+id)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var tail bytes.Buffer
	cmd.Stderr = &tail
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "KD"); ok && progress != nil {
			if f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "%")), 64); err == nil {
				progress(f)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, lastBytes(tail.Bytes(), 600))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "source.info.json"))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// FetchInfo reads a video's details without downloading it.
func FetchInfo(ctx context.Context, id string) (*Info, error) {
	if !VideoID.MatchString(id) {
		return nil, errors.New("bad video id")
	}
	raw, err := command(ctx, false, "yt-dlp", "--ignore-config", "--no-warnings", "--no-playlist", "--skip-download",
		"--dump-single-json", "--", "https://www.youtube.com/watch?v="+id).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp info: %w", err)
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// FetchCaptions gets manual captions as a VTT file, or returns "".
func FetchCaptions(ctx context.Context, id, dir, lang string) (string, error) {
	if !VideoID.MatchString(id) {
		return "", errors.New("bad video id")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9-]{1,20}$`).MatchString(lang) {
		return "", errors.New("bad language")
	}
	if err := run(ctx, false, "yt-dlp", "--ignore-config", "--no-warnings", "--skip-download", "--write-subs",
		"--sub-langs", lang, "--sub-format", "vtt", "-o", filepath.Join(dir, "caption.%(ext)s"),
		"--", "https://www.youtube.com/watch?v="+id); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "caption.*.vtt"))
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

// SearchResult is one YouTube hit.
type SearchResult struct {
	VideoID    string `json:"videoId"`
	Title      string `json:"title"`
	Channel    string `json:"channel"`
	DurationMs int64  `json:"durationMs"`
	Karaoke    bool   `json:"karaoke"`
}

// Search asks YouTube for results start+1..start+count.
func Search(ctx context.Context, query string, start, count int) ([]SearchResult, error) {
	query = strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, query)
	if len(query) > 200 {
		query = query[:200]
	}
	out, err := command(ctx, false, "yt-dlp", "--ignore-config", "--no-warnings", "--flat-playlist", "--dump-single-json",
		"--playlist-items", fmt.Sprintf("%d:%d", start+1, start+count),
		"--", fmt.Sprintf("ytsearch%d:%s", start+count, query)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("yt-dlp search: %s", lastBytes(ee.Stderr, 400))
		}
		return nil, err
	}
	var res struct {
		Entries []struct {
			ID       string  `json:"id"`
			Title    string  `json:"title"`
			Channel  string  `json:"channel"`
			Uploader string  `json:"uploader"`
			Duration float64 `json:"duration"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(res.Entries))
	for _, e := range res.Entries {
		if !VideoID.MatchString(e.ID) {
			continue
		}
		ch := e.Channel
		if ch == "" {
			ch = e.Uploader
		}
		results = append(results, SearchResult{VideoID: e.ID, Title: e.Title, Channel: ch, DurationMs: int64(e.Duration * 1000)})
	}
	return results, nil
}

// UpdateYtDlp runs yt-dlp's self-update, when it was installed standalone.
func UpdateYtDlp(ctx context.Context) (string, error) {
	out, err := command(ctx, false, "yt-dlp", "-U").CombinedOutput()
	return lastBytes(out, 400), err
}

// Versions reports which helper programs are installed.
func Versions(ctx context.Context) map[string]string {
	out := map[string]string{}
	for name, arg := range map[string]string{"yt-dlp": "--version", "ffmpeg": "-version", "deno": "--version", "uv": "--version"} {
		b, err := command(ctx, false, name, arg).Output()
		if err != nil {
			out[name] = "missing"
			continue
		}
		line, _, _ := strings.Cut(string(b), "\n")
		out[name] = strings.TrimSpace(line)
	}
	return out
}

// Encoder is one H.264 option from section 3.
type Encoder struct {
	Name   string
	Args   []string
	Format string
}

var allEncoders = []Encoder{
	{"qsv", []string{"-c:v", "h264_qsv", "-preset", "veryfast", "-global_quality", "23"}, "nv12"},
	{"nvenc", []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "23"}, "yuv420p"},
	{"x264", []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23"}, "yuv420p"},
}

// Encoders holds the options that passed a real test encode.
type Encoders struct {
	mu        sync.RWMutex
	available []Encoder
}

// Probe encodes a one-second clip with each option.
func (e *Encoders) Probe(ctx context.Context) []string {
	var ok []Encoder
	var names []string
	for _, enc := range allEncoders {
		args := append([]string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=320x240:d=1:r=25", "-vf", "format=" + enc.Format}, enc.Args...)
		args = append(args, "-f", "null", "-")
		if err := run(ctx, false, "ffmpeg", args...); err != nil {
			slog.Info("encoder unavailable", "encoder", enc.Name)
			continue
		}
		ok = append(ok, enc)
		names = append(names, enc.Name)
	}
	e.mu.Lock()
	e.available = ok
	e.mu.Unlock()
	return names
}

// Available lists working encoder names.
func (e *Encoders) Available() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.available))
	for _, x := range e.available {
		names = append(names, x.Name)
	}
	return names
}

func (e *Encoders) chain(choice string) []Encoder {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if choice == "" || choice == "auto" {
		return append([]Encoder(nil), e.available...)
	}
	var first, rest []Encoder
	for _, x := range e.available {
		if x.Name == choice {
			first = append(first, x)
		} else {
			rest = append(rest, x)
		}
	}
	return append(first, rest...)
}

// Encode tries each encoder in order until one succeeds.
func (e *Encoders) Encode(ctx context.Context, choice, src, dst string, height int, withAudio bool) error {
	chain := e.chain(choice)
	if len(chain) == 0 {
		return errors.New("no video encoder works on this machine")
	}
	var last error
	for _, enc := range chain {
		args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", src,
			"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)',format=%s", height, enc.Format)}
		args = append(args, enc.Args...)
		if withAudio {
			args = append(args, "-c:a", "aac", "-b:a", "128k")
		} else {
			args = append(args, "-an")
		}
		args = append(args, "-movflags", "+faststart", dst)
		if last = run(ctx, true, "ffmpeg", args...); last == nil {
			return nil
		}
		slog.Warn("encode failed, trying next", "encoder", enc.Name, "err", last)
		_ = os.Remove(dst)
	}
	return last
}

// ProbeVideo returns the first video stream's codec, pixel format and height.
func ProbeVideo(ctx context.Context, path string) (codec, pix string, height int, err error) {
	out, err := command(ctx, false, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,pix_fmt,height", "-of", "json", path).Output()
	if err != nil {
		return "", "", 0, err
	}
	var r struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Pix    string `json:"pix_fmt"`
			Height int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", "", 0, err
	}
	if len(r.Streams) == 0 {
		return "", "", 0, errors.New("no video stream")
	}
	s := r.Streams[0]
	return s.Codec, s.Pix, s.Height, nil
}

// HasVideo reports whether a file has a real video stream, not just cover art.
func HasVideo(ctx context.Context, path string) bool {
	out, err := command(ctx, false, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,disposition:stream_disposition=attached_pic", "-of", "default=nw=1", path).Output()
	if err != nil || len(out) == 0 {
		return false
	}
	return !strings.Contains(string(out), "attached_pic=1")
}

// ToSourceVideo turns an uploaded file into source.mp4. Audio-only files get a
// plain picture, so every song follows the same pipeline.
func ToSourceVideo(ctx context.Context, src, dst string) error {
	if HasVideo(ctx, src) {
		if err := run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:v:0", "-map", "0:a:0", "-c", "copy", dst); err == nil {
			return nil
		}
		return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:v:0", "-map", "0:a:0",
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", dst)
	}
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0x15121f:s=854x480:r=5", "-i", src,
		"-map", "0:v:0", "-map", "1:a:0", "-shortest", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "stillimage", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", dst)
}

// DurationMs returns a media file's length.
func DurationMs(ctx context.Context, path string) (int64, error) {
	out, err := command(ctx, false, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return int64(f * 1000), err
}

// PrepVideo copies H.264 when possible and encodes otherwise.
func (e *Encoders) PrepVideo(ctx context.Context, choice, src, dst string, height int) error {
	codec, pix, h, err := ProbeVideo(ctx, src)
	if err != nil {
		return err
	}
	if codec == "h264" && pix == "yuv420p" && h <= height {
		return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-c", "copy", "-movflags", "+faststart", dst)
	}
	return e.Encode(ctx, choice, src, dst, height, true)
}

// QuickAudio makes the center-cancelled karaoke track.
func QuickAudio(ctx context.Context, src, dst string) error {
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-vn",
		"-af", "pan=mono|c0=0.5*c0-0.5*c1,loudnorm=I=-16:TP=-1.5:LRA=11", "-c:a", "aac", "-b:a", "96k", "-ar", "44100", dst)
}

// PlainAudio makes the quick-mode file from a source that has no lead vocal.
func PlainAudio(ctx context.Context, src, dst string) error {
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-vn",
		"-af", "loudnorm=I=-16:TP=-1.5:LRA=11", "-c:a", "aac", "-b:a", "96k", "-ar", "44100", dst)
}

// SampleWav extracts three 10-second clips spread across a song into one file.
func SampleWav(ctx context.Context, src, dst string, durationMs int64) error {
	a, b, c := durationMs/4000, durationMs/2000, durationMs*3/4000
	filter := fmt.Sprintf("aselect='between(t,%d,%d)+between(t,%d,%d)+between(t,%d,%d)',asetpts=N/SR/TB", a, a+10, b, b+10, c, c+10)
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-vn", "-af", filter, "-ac", "2", "-ar", "44100", "-c:a", "pcm_s16le", dst)
}

// ToWav extracts stereo PCM for processing.
func ToWav(ctx context.Context, src, dst string) error {
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-vn", "-ac", "2", "-ar", "44100", "-c:a", "pcm_s16le", dst)
}

// Extension returns the file extension for an audio format.
func Extension(codec string) string {
	if codec == "opus" {
		return ".opus"
	}
	return ".m4a"
}

// Trim is what to cut from each end of a file of a given length.
type Trim struct {
	StartMs, EndMs, TotalMs int64
}

func (t Trim) filter() string {
	if t.StartMs == 0 && t.EndMs == 0 {
		return ""
	}
	return fmt.Sprintf("atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS,", float64(t.StartMs)/1000, float64(t.TotalMs-t.EndMs)/1000)
}

// PackageAudio makes the final level-matched file.
func PackageAudio(ctx context.Context, src, dst, codec string, kbps int, trim Trim) error {
	c := "aac"
	if codec == "opus" {
		c = "libopus"
	}
	return run(ctx, true, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", src, "-vn",
		"-af", trim.filter()+"loudnorm=I=-16:TP=-1.5:LRA=11", "-ar", "48000", "-c:a", c, "-b:a", fmt.Sprintf("%dk", kbps), "-ac", "2", dst)
}

var (
	silenceStart = regexp.MustCompile(`silence_start: (-?[\d.]+)`)
	silenceEnd   = regexp.MustCompile(`silence_end: ([\d.]+)`)
)

// SilentEnds reports how long a file is silent at its start and at its end.
func SilentEnds(ctx context.Context, src string, totalMs int64) (startMs, endMs int64, err error) {
	var log bytes.Buffer
	cmd := command(ctx, true, "ffmpeg", "-hide_banner", "-nostats", "-i", src, "-af", "silencedetect=noise=-45dB:d=0.5", "-f", "null", "-")
	cmd.Stderr = &log
	if err := startAndLower(cmd, true); err != nil {
		return 0, 0, err
	}
	if err := cmd.Wait(); err != nil {
		return 0, 0, err
	}
	startMs, endMs = parseSilentEnds(log.String(), totalMs)
	return startMs, endMs, nil
}

// parseSilentEnds reads silencedetect's log. A last silence with no end ran to
// the end of the file.
func parseSilentEnds(log string, totalMs int64) (startMs, endMs int64) {
	const edge = 100 // a silence this close to the edge touches it
	ms := func(s string) int64 {
		f, _ := strconv.ParseFloat(s, 64)
		return int64(f * 1000)
	}
	starts := silenceStart.FindAllStringSubmatch(log, -1)
	ends := silenceEnd.FindAllStringSubmatch(log, -1)
	if len(starts) == 0 {
		return 0, 0
	}
	if ms(starts[0][1]) <= edge && len(ends) > 0 {
		startMs = ms(ends[0][1])
	}
	last := len(starts) - 1
	lastStart := ms(starts[last][1])
	if lastStart <= edge {
		return startMs, 0 // the only silence is the one at the start
	}
	if len(ends) == last || ms(ends[last][1]) >= totalMs-edge {
		endMs = max(totalMs-lastStart, 0)
	}
	return startMs, endMs
}

// MonoSamples decodes a file to mono 16-bit samples at the given rate.
func MonoSamples(ctx context.Context, path string, rate int) ([]int16, error) {
	raw, err := command(ctx, true, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "s16le", "-").Output()
	if err != nil {
		return nil, fmt.Errorf("decode audio: %w", err)
	}
	out := make([]int16, len(raw)/2)
	for i := range out {
		out[i] = int16(uint16(raw[i*2]) | uint16(raw[i*2+1])<<8)
	}
	return out, nil
}

// Envelope decodes the vocal-heavy mid channel into loudness at 10 per second.
func Envelope(ctx context.Context, src string) ([]float64, error) {
	const rate = 4000
	cmd := command(ctx, true, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", src, "-vn",
		"-af", "pan=mono|c0=0.5*c0+0.5*c1,highpass=f=300,lowpass=f=1800", "-ar", strconv.Itoa(rate), "-f", "s16le", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	const window = rate / 10
	r := bufio.NewReaderSize(stdout, 1<<16)
	var env []float64
	var sum float64
	n := 0
	buf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		v := float64(int16(binary.LittleEndian.Uint16(buf))) / 32768
		sum += v * v
		n++
		if n == window {
			env = append(env, math.Sqrt(sum/float64(n)))
			sum, n = 0, 0
		}
	}
	return env, cmd.Wait()
}

// AccentColor picks a vivid color from a JPEG.
func AccentColor(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}
	type bucket struct{ r, g, b, w float64 }
	var hues [24]bucket
	b := img.Bounds()
	stepX, stepY := max(b.Dx()/64, 1), max(b.Dy()/64, 1)
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			r32, g32, b32, _ := img.At(x, y).RGBA()
			r, g, bl := float64(r32>>8)/255, float64(g32>>8)/255, float64(b32>>8)/255
			h, s, v := toHSV(r, g, bl)
			w := s * s * v
			i := int(h/15) % 24
			hues[i].r += r * w
			hues[i].g += g * w
			hues[i].b += bl * w
			hues[i].w += w
		}
	}
	best := 0
	for i := range hues {
		if hues[i].w > hues[best].w {
			best = i
		}
	}
	if hues[best].w < 0.5 {
		return "#7c5cff", nil // a gray image gets the app's default
	}
	h := hues[best]
	hue, s, v := toHSV(h.r/h.w, h.g/h.w, h.b/h.w)
	r, g, bl := fromHSV(hue, math.Max(s, 0.55), math.Max(v, 0.9))
	return fmt.Sprintf("#%02x%02x%02x", int(r*255), int(g*255), int(bl*255)), nil
}

func toHSV(r, g, b float64) (h, s, v float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	v = mx
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, v
}

func fromHSV(h, s, v float64) (r, g, b float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	switch int(h/60) % 6 {
	case 0:
		r, g = c, x
	case 1:
		r, g = x, c
	case 2:
		g, b = c, x
	case 3:
		g, b = x, c
	case 4:
		r, b = x, c
	default:
		r, b = c, x
	}
	return r + m, g + m, b + m
}

// SaveCoverImage decodes an uploaded image, crops and scales it to a square
// JPEG up to 500x500, and saves it atomically to dst.
func SaveCoverImage(ctx context.Context, r io.Reader, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil || len(raw) == 0 {
		return errors.New("empty image")
	}
	tmp := dst + ".tmp"
	if img, _, err := image.Decode(bytes.NewReader(raw)); err == nil {
		b := img.Bounds()
		side := min(b.Dx(), b.Dy())
		if side <= 0 {
			return errors.New("bad image dimensions")
		}
		ox := b.Min.X + (b.Dx()-side)/2
		oy := b.Min.Y + (b.Dy()-side)/2
		outSide := min(side, 500)
		dstImg := image.NewRGBA(image.Rect(0, 0, outSide, outSide))
		for y := range outSide {
			sy := oy + y*side/outSide
			for x := range outSide {
				sx := ox + x*side/outSide
				dstImg.Set(x, y, img.At(sx, sy))
			}
		}
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		err = jpeg.Encode(f, dstImg, &jpeg.Options{Quality: 88})
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, dst)
	}
	inPath := dst + ".in"
	if err := os.WriteFile(inPath, raw, 0o644); err != nil {
		return err
	}
	defer os.Remove(inPath)
	vf := "scale='min(500,iw)':'min(500,ih)':force_original_aspect_ratio=increase,crop='min(iw,ih)':'min(iw,ih)'"
	if err := run(ctx, false, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", inPath, "-frames:v", "1", "-vf", vf, "-f", "image2", "-c:v", "mjpeg", "-q:v", "3", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
