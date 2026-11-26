package tools

import "testing"

func TestParseSilentEnds(t *testing.T) {
	cases := []struct {
		name       string
		log        string
		start, end int64
	}{
		{"none", "", 0, 0},
		{"at the start", "silence_start: 0\nsilence_end: 3.5 | silence_duration: 3.5\n", 3500, 0},
		{"at the end, unfinished", "silence_start: 95\n", 0, 5000},
		{"at the end, finished", "silence_start: 95\nsilence_end: 100 | silence_duration: 5\n", 0, 5000},
		{"both, gap in the middle", "silence_start: 0\nsilence_end: 2 | d\nsilence_start: 50\nsilence_end: 52 | d\nsilence_start: 97\n", 2000, 3000},
		{"only in the middle", "silence_start: 50\nsilence_end: 52 | d\n", 0, 0},
	}
	for _, c := range cases {
		if s, e := parseSilentEnds(c.log, 100000); s != c.start || e != c.end {
			t.Errorf("%s: got %d,%d want %d,%d", c.name, s, e, c.start, c.end)
		}
	}
}

func TestTrimFilter(t *testing.T) {
	if f := (Trim{}).filter(); f != "" {
		t.Errorf("no trim: %q", f)
	}
	if f := (Trim{StartMs: 1500, EndMs: 2000, TotalMs: 10000}).filter(); f != "atrim=start=1.500:end=8.000,asetpts=PTS-STARTPTS," {
		t.Errorf("%q", f)
	}
}
