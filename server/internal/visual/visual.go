// Package visual works out what the TV background needs to follow a song: how
// loud it is over time, where its beats and sections are, and the colours of its
// album. It runs once and is saved, so the TV never analyses audio itself.
package visual

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // decode album covers
	_ "image/png"
	"math"
	"math/cmplx"
	"os"
	"slices"
)

// SampleRate is the rate Analyze expects.
const SampleRate = 11025

const (
	frameSize  = 1024
	hop        = 551 // about 20 readings a second
	bassTop    = 18  // bins of about 11 Hz, so up to about 200 Hz
	midTop     = 185 // up to about 2 kHz
	sectionMin = 6.0 // seconds before the energy may start a new section
	sectionMax = 28.0
	sectionGap = 0.18 // how far the energy must move to start a new section
)

// Section is a stretch of the song with its own look.
type Section struct {
	StartMs int64   `json:"startMs"`
	Energy  float64 `json:"energy"`
}

// Data is the saved analysis. Each band holds one byte per reading.
type Data struct {
	Version  int       `json:"version"`
	Rate     float64   `json:"rate"`
	Bass     []byte    `json:"bass"`
	Mid      []byte    `json:"mid"`
	High     []byte    `json:"high"`
	Level    []byte    `json:"level"`
	Beats    []int64   `json:"beats"`
	Sections []Section `json:"sections"`
}

// Analyze reads a song as mono samples at SampleRate.
func Analyze(samples []int16) *Data {
	d := &Data{Version: 1, Rate: float64(SampleRate) / hop, Beats: []int64{}, Sections: []Section{}}
	if len(samples) < frameSize {
		return d
	}
	frames := (len(samples)-frameSize)/hop + 1
	bass, mid, high, level := make([]float64, frames), make([]float64, frames), make([]float64, frames), make([]float64, frames)
	window := make([]float64, frameSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/(frameSize-1))
	}
	buf := make([]complex128, frameSize)
	for f := range frames {
		start := f * hop
		for i := range buf {
			buf[i] = complex(float64(samples[start+i])/32768*window[i], 0)
		}
		fft(buf)
		var b, m, h float64
		for k := 1; k < frameSize/2; k++ {
			p := real(buf[k])*real(buf[k]) + imag(buf[k])*imag(buf[k])
			switch {
			case k <= bassTop:
				b += p
			case k <= midTop:
				m += p
			default:
				h += p
			}
		}
		bass[f], mid[f], high[f], level[f] = math.Log1p(b), math.Log1p(m), math.Log1p(h*4), math.Log1p(b+m+h)
	}
	normalize(bass)
	normalize(mid)
	normalize(high)
	normalize(level)
	d.Bass, d.Mid, d.High, d.Level = toBytes(bass), toBytes(mid), toBytes(high), toBytes(level)
	d.Beats = findBeats(bass, mid, d.Rate)
	d.Sections = findSections(level, d.Rate)
	return d
}

// fft is an in-place radix-2 transform. len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Rect(1, -2*math.Pi/float64(size))
		for i := 0; i < n; i += size {
			w := complex(1, 0)
			for j := range size / 2 {
				u, v := a[i+j], a[i+j+size/2]*w
				a[i+j], a[i+j+size/2] = u+v, u-v
				w *= step
			}
		}
	}
}

// normalize scales values to 0..1 between the song's own quiet and loud, so
// every song uses the full range.
func normalize(v []float64) {
	sorted := slices.Clone(v)
	slices.Sort(sorted)
	lo, hi := sorted[len(sorted)*5/100], sorted[len(sorted)*98/100]
	for i := range v {
		if hi <= lo {
			v[i] = 0
			continue
		}
		v[i] = math.Max(0, math.Min(1, (v[i]-lo)/(hi-lo)))
	}
}

func toBytes(v []float64) []byte {
	out := make([]byte, len(v))
	for i, x := range v {
		out[i] = byte(x*255 + 0.5)
	}
	return out
}

// findBeats picks moments where the bass and mids jump above what's around them.
func findBeats(bass, mid []float64, rate float64) []int64 {
	const around, minGap = 10, 4
	flux := make([]float64, len(bass))
	for i := 1; i < len(bass); i++ {
		flux[i] = math.Max(0, bass[i]-bass[i-1]) + 0.5*math.Max(0, mid[i]-mid[i-1])
	}
	beats := []int64{}
	last := -minGap
	for i := 1; i+1 < len(flux); i++ {
		sum := 0.0
		from, to := max(i-around, 0), min(i+around+1, len(flux))
		for _, f := range flux[from:to] {
			sum += f
		}
		if flux[i] > 1.4*sum/float64(to-from)+0.04 && flux[i] >= flux[i-1] && flux[i] >= flux[i+1] && i-last >= minGap {
			beats = append(beats, int64(float64(i)*1000/rate))
			last = i
		}
	}
	return beats
}

// findSections splits the song where its energy shifts, and never lets one look
// run too long.
func findSections(level []float64, rate float64) []Section {
	window := int(3 * rate)
	smooth := make([]float64, len(level))
	sum := 0.0
	for i, v := range level {
		sum += v
		if i >= window {
			sum -= level[i-window]
		}
		smooth[i] = sum / float64(min(i+1, window))
	}
	sections := []Section{{StartMs: 0, Energy: smooth[0]}}
	start, total, count := 0, 0.0, 0
	for i := 0; i < len(smooth); i += int(rate) {
		total += smooth[i]
		count++
		age := float64(i-start) / rate
		if age < sectionMin {
			continue
		}
		if math.Abs(smooth[i]-total/float64(count)) > sectionGap || age >= sectionMax {
			sections = append(sections, Section{StartMs: int64(float64(i) * 1000 / rate), Energy: smooth[i]})
			start, total, count = i, smooth[i], 1
		}
	}
	return sections
}

// Palette is an album's colours for the background.
type Palette struct {
	Background string   `json:"background"`
	Colors     []string `json:"colors"`
}

type hsv struct{ h, s, v float64 }

func toHSV(r, g, b float64) hsv {
	high, low := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := high - low
	c := hsv{v: high}
	if high > 0 {
		c.s = d / high
	}
	if d == 0 {
		return c
	}
	switch high {
	case r:
		c.h = math.Mod((g-b)/d+6, 6)
	case g:
		c.h = (b-r)/d + 2
	default:
		c.h = (r-g)/d + 4
	}
	c.h /= 6
	return c
}

func (c hsv) hex() string {
	h := math.Mod(c.h+1, 1) * 6
	x := c.v * c.s
	m := c.v - x
	y := x * (1 - math.Abs(math.Mod(h, 2)-1))
	var r, g, b float64
	switch int(h) {
	case 0:
		r, g = x, y
	case 1:
		r, g = y, x
	case 2:
		g, b = x, y
	case 3:
		g, b = y, x
	case 4:
		r, b = y, x
	default:
		r, b = x, y
	}
	to := func(v float64) int { return int(math.Round((v + m) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", to(r), to(g), to(b))
}

// PickPalette finds an album's main colours and brightens them for dots on dark.
func PickPalette(img image.Image) Palette {
	const buckets = 24
	type bucket struct{ r, g, b, w float64 }
	var hues [buckets]bucket
	bounds := img.Bounds()
	step := max(bounds.Dx()/80, 1)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			r, g, b := float64(pr)/65535, float64(pg)/65535, float64(pb)/65535
			c := toHSV(r, g, b)
			w := c.s * c.v
			i := int(c.h*buckets) % buckets
			hues[i].r += r * w
			hues[i].g += g * w
			hues[i].b += b * w
			hues[i].w += w
		}
	}
	order := make([]int, buckets)
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		switch {
		case hues[a].w > hues[b].w:
			return -1
		case hues[a].w < hues[b].w:
			return 1
		}
		return a - b
	})
	var picked []hsv
	var used []int
	for _, i := range order {
		if hues[i].w < 0.5 || len(picked) == 3 {
			break
		}
		near := false
		for _, u := range used {
			if gap := min((i-u+buckets)%buckets, (u-i+buckets)%buckets); gap < 2 {
				near = true
			}
		}
		if near {
			continue
		}
		b := hues[i]
		c := toHSV(b.r/b.w, b.g/b.w, b.b/b.w)
		c.s, c.v = math.Max(c.s, 0.55), math.Max(c.v, 0.85)
		picked = append(picked, c)
		used = append(used, i)
	}
	if len(picked) == 0 { // a black and white cover
		picked = []hsv{{h: 0.94, s: 0.7, v: 1}}
	}
	for len(picked) < 3 { // neighbours of the main colour fill any gaps
		c := picked[0]
		c.h += 0.08 * float64(len(picked)*2-3)
		picked = append(picked, c)
	}
	p := Palette{Background: hsv{h: picked[0].h, s: math.Min(picked[0].s, 0.6), v: 0.1}.hex()}
	for _, c := range picked {
		p.Colors = append(p.Colors, c.hex())
	}
	return p
}

// PaletteJSON reads a cover image and returns its palette as JSON.
func PaletteJSON(coverPath string) ([]byte, error) {
	f, err := os.Open(coverPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return json.Marshal(PickPalette(img))
}
