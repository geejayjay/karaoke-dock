// Package settings defines every admin-editable setting and validates changes.
package settings

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"karaokedock/internal/store"
)

// Def describes one setting.
type Def struct {
	Key      string   `json:"key"`
	Page     string   `json:"page"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // text, bool, choice, number
	Choices  []string `json:"choices,omitempty"`
	Default  string   `json:"default"`
	Restart  bool     `json:"restart,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
	validate func(string) error
}

func choice(key, page, label, def string, adv bool, c ...string) Def {
	return Def{Key: key, Page: page, Label: label, Kind: "choice", Choices: c, Default: def, Advanced: adv}
}
func boolean(key, page, label, def string) Def {
	return Def{Key: key, Page: page, Label: label, Kind: "bool", Choices: []string{"true", "false"}, Default: def}
}
func number(key, page, label, def string, lo, hi int, adv bool) Def {
	return Def{Key: key, Page: page, Label: label, Kind: "number", Default: def, Advanced: adv, validate: func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("must be a whole number from %d to %d", lo, hi)
		}
		return nil
	}}
}
func text(key, page, label, def string, adv bool, v func(string) error) Def {
	return Def{Key: key, Page: page, Label: label, Kind: "text", Default: def, Advanced: adv, validate: v}
}

func validURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("enter a full address such as https://karaoke.example.com")
	}
	return nil
}

func validIP(v string) error {
	if v != "" && net.ParseIP(v) == nil {
		return fmt.Errorf("enter an IP address, or leave empty to detect it")
	}
	return nil
}

func validPin(v string) error {
	if len(v) < 4 || len(v) > 12 {
		return fmt.Errorf("must be 4 to 12 digits")
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return fmt.Errorf("must be digits only")
		}
	}
	return nil
}

func validRoom(v string) error {
	if len(v) < 4 || len(v) > 16 {
		return fmt.Errorf("must be 4 to 16 letters or digits")
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return fmt.Errorf("must be letters or digits only")
		}
	}
	return nil
}

func validModelName(v string) error {
	if v == "" || strings.ContainsAny(v, `/\`) || strings.Contains(v, "..") {
		return fmt.Errorf("enter a model file name")
	}
	return nil
}

// Keys used by other packages.
const (
	QRMode           = "QR_MODE"
	PublicURL        = "PUBLIC_URL"
	LanIP            = "LAN_IP"
	UnprocessedAudio = "UNPROCESSED_AUDIO"
	DefaultVocalMode = "DEFAULT_VOCAL_MODE"
	AnimationLevel   = "ANIMATION_LEVEL"
	Countdown        = "COUNTDOWN"
	CountdownSeconds = "COUNTDOWN_SECONDS"
	MLDevice         = "ML_DEVICE"
	SeparationModel  = "SEPARATION_MODEL"
	WhisperModel     = "WHISPER_MODEL"
	VideoEncoder     = "VIDEO_ENCODER"
	MLThreads        = "ML_THREADS"
	PauseImports     = "PAUSE_IMPORTS"
	AudioCodec       = "AUDIO_CODEC"
	AudioBitrate     = "AUDIO_BITRATE"
	KeepGuide        = "KEEP_GUIDE"
	VideoKeep        = "VIDEO_KEEP"
	MediaRoot        = "MEDIA_ROOT"
	TempRoot         = "TEMP_ROOT"
	RoomCode         = "ROOM_CODE"
	HostPin          = "HOST_PIN"
	QueueLimit       = "QUEUE_LIMIT"
	ImportLimit      = "IMPORT_LIMIT"
	AdminRemote      = "ADMIN_REMOTE"
	TrustedProxy     = "TRUSTED_PROXY"
)

// Defs is the full list, shown by page in the admin panel.
var Defs = []Def{
	choice(QRMode, "Network", "QR code uses", "lan", false, "lan", "domain", "auto"),
	text(PublicURL, "Network", "Domain address", "", false, validURL),
	text(LanIP, "Network", "LAN IP (empty to detect)", "", false, validIP),

	choice(UnprocessedAudio, "Playback", "Audio for songs still processing", "quick", false, "quick", "original"),
	choice(DefaultVocalMode, "Playback", "Default vocals", "off", false, "off", "guide"),
	choice(AnimationLevel, "Playback", "Animation", "auto", false, "auto", "full", "lite"),
	boolean(Countdown, "Playback", "Countdown before lyrics", "true"),
	number(CountdownSeconds, "Playback", "Countdown length (seconds)", "10", 1, 60, false),

	choice(MLDevice, "Processing", "Processor", "auto", true, "auto", "cuda", "cpu", "openvino"),
	text(SeparationModel, "Processing", "Vocal removal model", "Kim_Vocal_2.onnx", true, validModelName),
	choice(WhisperModel, "Processing", "Lyric timing model", "base", true, "tiny", "base", "small", "medium"),
	choice(VideoEncoder, "Processing", "Video encoder", "auto", true, "auto", "qsv", "nvenc", "x264"),
	number(MLThreads, "Processing", "Processing threads (0 for automatic)", "0", 0, 256, true),
	boolean(PauseImports, "Processing", "Pause imports", "false"),

	text(MediaRoot, "Storage", "Audio and lyrics folder", "", false, nil),
	text(TempRoot, "Storage", "Temporary folder", "", false, nil),
	choice(AudioCodec, "Storage", "Audio format", "aac", true, "aac", "opus"),
	number(AudioBitrate, "Storage", "Audio bitrate (kbps)", "96", 48, 320, true),
	boolean(KeepGuide, "Storage", "Keep guide vocals", "false"),
	choice(VideoKeep, "Storage", "Keep videos", "off", false, "off", "480p", "720p"),

	text(RoomCode, "Access", "Room code", "", false, validRoom),
	text(HostPin, "Access", "Host PIN", "", false, validPin),
	number(QueueLimit, "Access", "Songs per singer in the queue", "5", 1, 100, false),
	number(ImportLimit, "Access", "New songs per guest per hour", "20", 1, 1000, false),
	boolean(AdminRemote, "Access", "Allow admin from outside the LAN", "false"),
	text(TrustedProxy, "Access", "Trusted proxy address", "", true, validIP),
}

func def(key string) *Def {
	for i := range Defs {
		if Defs[i].Key == key {
			return &Defs[i]
		}
	}
	return nil
}

// Manager caches settings in memory and persists changes.
type Manager struct {
	db *store.Store
	mu sync.RWMutex
	v  map[string]string
}

// Load reads settings, filling in defaults and generating secrets on first use.
func Load(ctx context.Context, db *store.Store, defaults map[string]string) (*Manager, error) {
	stored, err := db.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	m := &Manager{db: db, v: map[string]string{}}
	missing := map[string]string{}
	for _, d := range Defs {
		v, ok := stored[d.Key]
		if !ok {
			v = d.Default
			if dv, has := defaults[d.Key]; has {
				v = dv
			}
			switch d.Key {
			case RoomCode:
				v = randomString("ABCDEFGHJKMNPQRSTUVWXYZ23456789", 6)
			case HostPin:
				v = randomString("0123456789", 6)
			}
			missing[d.Key] = v
		}
		m.v[d.Key] = v
	}
	for k, v := range stored { // keep private keys such as the admin hash
		m.v[k] = v
	}
	if len(missing) > 0 {
		if err := db.SetSettings(ctx, missing); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func randomString(alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[x.Int64()]
	}
	return string(b)
}

// Get returns a value.
func (m *Manager) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.v[key]
}

// Bool returns a boolean value.
func (m *Manager) Bool(key string) bool { return m.Get(key) == "true" }

// Int returns a number value.
func (m *Manager) Int(key string) int {
	n, _ := strconv.Atoi(m.Get(key))
	return n
}

// All returns the editable settings.
func (m *Manager) All() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(Defs))
	for _, d := range Defs {
		out[d.Key] = m.v[d.Key]
	}
	return out
}

// Validate checks one value against its definition.
func Validate(key, value string) error {
	d := def(key)
	if d == nil {
		return fmt.Errorf("unknown setting")
	}
	if d.Choices != nil {
		for _, c := range d.Choices {
			if c == value {
				return nil
			}
		}
		return fmt.Errorf("choose one of: %s", strings.Join(d.Choices, ", "))
	}
	if d.validate != nil {
		return d.validate(value)
	}
	return nil
}

// Patch validates and saves changes. It returns field errors, if any, and
// whether a restart is needed.
func (m *Manager) Patch(ctx context.Context, kv map[string]string) (fieldErrs map[string]string, restart bool, err error) {
	fieldErrs = map[string]string{}
	for k, v := range kv {
		if e := Validate(k, v); e != nil {
			fieldErrs[k] = e.Error()
		}
		if d := def(k); d != nil && d.Restart && m.Get(k) != v {
			restart = true
		}
	}
	if len(fieldErrs) > 0 {
		return fieldErrs, false, nil
	}
	if err := m.SetRaw(ctx, kv); err != nil {
		return nil, false, err
	}
	return nil, restart, nil
}

// SetRaw saves values with no validation, for internal keys.
func (m *Manager) SetRaw(ctx context.Context, kv map[string]string) error {
	if err := m.db.SetSettings(ctx, kv); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range kv {
		m.v[k] = v
	}
	return nil
}
