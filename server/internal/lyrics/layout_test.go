package lyrics

import (
	"strings"
	"testing"
)

func texts(d *Doc) []string {
	out := make([]string, len(d.Lines))
	for i, l := range d.Lines {
		out[i] = l.Text()
	}
	return out
}

func TestReflowFollowsTheRows(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("I cant save us my Atlantis we fall", 1000, 9000), NewLine("We built this", 10000, 13000)}}
	timed := d.Reflow([]Row{{Words: 4}, {Words: 4, WordTimes: [][2]int64{{5000, 5500}, {5500, 6000}, {6000, 6500}, {6500, 7000}}}, {Words: 3}})
	got := texts(d)
	if len(got) != 3 || got[0] != "I cant save us" || got[1] != "my Atlantis we fall" || got[2] != "We built this" || timed != 1 {
		t.Fatalf("%v timed %d", got, timed)
	}
	if d.Lines[1].StartMs != 5000 || d.Lines[1].EndMs != 7000 || d.Lines[2].StartMs != 10000 {
		t.Errorf("%+v", d.Lines)
	}
}

func TestLinesTimedFromTheVideoAreMarkedAndKeptWhenBroken(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one two", 0, 1000), NewLine("three four", 1000, 2000), NewLine("five", 2000, 3000)}}
	d.Reflow([]Row{{Words: 2, WordTimes: [][2]int64{{100, 200}, {200, 300}}}, {Words: 3}})
	if !d.Lines[0].Exact || d.Lines[1].Exact || d.ExactLines() != 1 || !d.MostlyFromVideo() {
		t.Fatalf("%+v", d.Lines)
	}
	long := NewLine("this line is much too long to fit on the screen in one go", 0, 6000)
	long.Exact = true
	broken := &Doc{Lines: []Line{long}}
	broken.BreakLong()
	if len(broken.Lines) < 2 || broken.ExactLines() != len(broken.Lines) {
		t.Errorf("%+v", broken.Lines)
	}
}

func TestExactLinesAreNeverRepaired(t *testing.T) {
	exact := NewLine("from the video", 90000, 92000)
	exact.Exact = true
	before := []Line{NewLine("a", 1000, 2000), NewLine("b", 3000, 4000), exact, NewLine("c", 93000, 94000), NewLine("d", 95000, 96000)}
	timed := []Line{NewLine("a", 21000, 22000), NewLine("b", 23000, 24000), exact, NewLine("c", 113000, 114000), NewLine("d", 115000, 116000)}
	RepairTiming(timed, before)
	if timed[2].StartMs != 90000 || timed[2].EndMs != 92000 {
		t.Errorf("%+v", timed[2])
	}
}

func TestLinesWithNoWipeFitBetweenExactOnes(t *testing.T) {
	a, b := NewLine("one", 1000, 2000), NewLine("four", 8000, 9000)
	a.Exact, b.Exact = true, true
	lines := []Line{a, NewLine("two", 500, 3000), NewLine("three", 3000, 12000), b}
	FitUntimed(lines)
	if lines[1].StartMs < 2000 || lines[2].EndMs > 8000 || lines[0].StartMs != 1000 || lines[3].StartMs != 8000 {
		t.Errorf("%+v", lines)
	}
}

func TestReflowLeavesLinesAloneIfWordsDontAddUp(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one two three", 0, 3000)}}
	if d.Reflow([]Row{{Words: 2}}); len(d.Lines) != 1 || d.Lines[0].Text() != "one two three" {
		t.Errorf("%v", texts(d))
	}
}

func TestReflowKeepsSingersApart(t *testing.T) {
	a, b := NewLine("one two", 0, 2000), NewLine("three four", 2000, 4000)
	a.Voice, b.Voice = "a", "b"
	d := &Doc{Lines: []Line{a, b}}
	d.Reflow([]Row{{Words: 4}})
	if len(d.Lines) != 2 || d.Lines[0].Voice != "a" || d.Lines[1].Voice != "b" {
		t.Errorf("%+v", d.Lines)
	}
}

func TestNotesThatArentSungAreRemoved(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("(Chorus)", 0, 1000), NewLine("Hold on (x2)", 1000, 3000), NewLine("(ooh baby)", 3000, 5000), NewLine("[Verse 2]", 5000, 6000)}}
	d.DropDirections()
	got := texts(d)
	if len(got) != 2 || got[0] != "Hold on" || got[1] != "(ooh baby)" {
		t.Fatalf("%v", got)
	}
}

func TestBackingVocalsDontForceOrSplitBreaks(t *testing.T) {
	// Too long as plain text, but the bracketed part has its own small row.
	d := &Doc{Lines: []Line{NewLine("We were running through the night (never coming home again)", 0, 6000)}}
	d.BreakLong()
	if len(d.Lines) != 1 {
		t.Fatalf("should fit: %v", texts(d))
	}
	row := &Doc{Lines: []Line{NewLine("turn the pain into power)", 0, 2000)}}
	if flags, _ := backingWords(row.Lines[0].Words); !flags[0] || !flags[4] {
		t.Errorf("a close with no open is a bracket from the row before: %v", flags)
	}
}

func exactRow(words int, start int64, role string) Row {
	r := Row{Words: words, Role: role}
	for i := range words {
		at := start + int64(i)*500
		r.WordTimes = append(r.WordTimes, [2]int64{at, at + 500})
	}
	return r
}

func TestBackingRowsAttachToTheLineTheyOverlap(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one two three", 0, 3000), NewLine("ooh aah", 3000, 4000), NewLine("four five six", 8000, 11000)}}
	d.Reflow([]Row{exactRow(3, 1000, ""), exactRow(2, 1200, "backing"), exactRow(3, 8000, "")})
	if len(d.Lines) != 2 || len(d.Lines[0].Backing) != 2 || d.Lines[0].Backing[0].StartMs != 1200 || d.Lines[1].Backing != nil {
		t.Fatalf("%+v", d.Lines)
	}
}

func TestAnEchoAttachesToTheLineBeforeIt(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one two", 0, 2000), NewLine("ooh aah", 3000, 4000), NewLine("three four", 9000, 11000)}}
	d.Reflow([]Row{exactRow(2, 1000, ""), exactRow(2, 3000, "backing"), exactRow(2, 9000, "")})
	if len(d.Lines) != 2 || len(d.Lines[0].Backing) != 2 {
		t.Fatalf("%+v", d.Lines)
	}
}

func TestLoneBackingRowStaysItsOwnLine(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("ooh aah", 0, 1000), NewLine("one two", 30000, 32000)}}
	d.Reflow([]Row{exactRow(2, 0, "backing"), exactRow(2, 30000, "")})
	if len(d.Lines) != 2 || !d.Lines[0].BackingOnly || d.Lines[1].BackingOnly {
		t.Fatalf("%+v", d.Lines)
	}
}

func TestVideoColoursSetTheSingers(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one two", 0, 2000), NewLine("three four", 3000, 5000)}}
	boy, girl := exactRow(2, 0, ""), exactRow(2, 3000, "")
	boy.Voice, girl.Voice = "boy", "girl"
	d.Reflow([]Row{boy, girl})
	if d.Lines[0].Voice != "a" || d.Lines[1].Voice != "b" || len(d.Voices) != 2 || d.Voices[0].Label != "Boy" || d.Voices[1].Source != "video" {
		t.Fatalf("%+v %+v", d.Lines, d.Voices)
	}
}

func TestTTMLBackingVocalsAreKeptSeparately(t *testing.T) {
	ttml := `<tt><body><div><p begin="1.0" end="4.0" agent="v1"><span begin="1.0" end="2.0">Hello</span><span role="x-bg"><span begin="2.0" end="3.0">(ooh)</span></span><span role="x-translation">skip</span></p></div></body></tt>`
	lines, _, err := ParseTTML(strings.NewReader(ttml))
	if err != nil || len(lines) != 1 || len(lines[0].Words) != 1 || len(lines[0].Backing) != 1 || lines[0].Backing[0].Text != "(ooh)" {
		t.Fatalf("%v %+v", err, lines)
	}
}

func TestRowsWithNoExactTimesFitBetweenTheOthers(t *testing.T) {
	d := &Doc{Lines: []Line{NewLine("one", 1000, 2000), NewLine("two", 30000, 31000), NewLine("three", 40000, 41000)}}
	d.Reflow([]Row{
		{Words: 1, WordTimes: [][2]int64{{1000, 2000}}},
		{Words: 1},
		{Words: 1, WordTimes: [][2]int64{{9000, 10000}}},
	})
	if d.Lines[1].StartMs < 2000 || d.Lines[1].EndMs > 9000 {
		t.Errorf("%+v", d.Lines)
	}
}

func TestBreakLongSplitsAtNaturalPlaces(t *testing.T) {
	d := &Doc{Lines: []Line{
		NewLine("I found you in the darkness, and you never let me go", 0, 6000),
		NewLine("A short line", 7000, 8000),
	}}
	d.BreakLong()
	got := texts(d)
	if len(got) != 3 || got[0] != "I found you in the darkness," || got[1] != "and you never let me go" || got[2] != "A short line" {
		t.Fatalf("%v", got)
	}
	if d.Lines[0].StartMs != 0 || d.Lines[1].EndMs != 6000 || d.Lines[0].EndMs > d.Lines[1].StartMs {
		t.Errorf("%+v", d.Lines)
	}
	before := len(d.Lines)
	if d.BreakLong(); len(d.Lines) != before {
		t.Error("breaking twice should change nothing")
	}
}
