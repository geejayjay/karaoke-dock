package visual

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestAnalyzeFollowsTheSong(t *testing.T) {
	// Eight quiet seconds, then 110 Hz bursts twice a second.
	samples := make([]int16, SampleRate*24)
	for i := range samples {
		sec := float64(i) / SampleRate
		if sec >= 8 && math.Mod(sec, 0.5) < 0.12 {
			samples[i] = int16(18000 * math.Sin(2*math.Pi*110*sec))
		}
	}
	d := Analyze(samples)
	if len(d.Bass) == 0 || len(d.Bass) != len(d.High) || math.Abs(d.Rate-20) > 0.1 {
		t.Fatalf("bands %d/%d rate %v", len(d.Bass), len(d.High), d.Rate)
	}
	if d.Level[int(4*d.Rate)] > 20 {
		t.Errorf("silence should be low: %d", d.Level[int(4*d.Rate)])
	}
	if len(d.Beats) < 20 || len(d.Beats) > 40 {
		t.Errorf("expected about 32 beats, got %d", len(d.Beats))
	}
	if d.Beats[0] < 7800 {
		t.Errorf("beat during silence at %d", d.Beats[0])
	}
	if len(d.Sections) < 2 {
		t.Errorf("sections: %v", d.Sections)
	}
}

func TestAnalyzeShortInput(t *testing.T) {
	if d := Analyze(make([]int16, 10)); len(d.Level) != 0 {
		t.Errorf("%+v", d)
	}
}

func TestPaletteTakesAlbumColours(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			img.Set(x, y, color.RGBA{200, 20, 30, 255})
			if x > 60 {
				img.Set(x, y, color.RGBA{20, 40, 200, 255})
			}
		}
	}
	p := PickPalette(img)
	if len(p.Colors) != 3 || p.Colors[0][1:3] < "c0" {
		t.Errorf("%+v", p)
	}
	if blank := PickPalette(image.NewGray(image.Rect(0, 0, 40, 40))); len(blank.Colors) != 3 {
		t.Errorf("%+v", blank)
	}
}
