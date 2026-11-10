package lyrics

import "testing"

func TestPickRecordingSkipsOtherVersions(t *testing.T) {
	recordings := []mbRecording{
		{ID: "slowed", Title: "Atlantis (slowed down version)", Score: 100, Length: 259000},
		{ID: "studio", Title: "Atlantis", Score: 100, Length: 205000},
		{ID: "other", Title: "Something Else", Score: 100, Length: 259000},
	}
	if got := pickRecording(recordings, "Atlantis", 259000); got == nil || got.ID != "studio" {
		t.Fatalf("got %+v", got)
	}
	if got := pickRecording(recordings, "Atlantis slowed down version", 259000); got == nil || got.ID != "slowed" {
		t.Fatalf("asked for it by name: got %+v", got)
	}
}

func TestLongMusicVideoStillMatchesTheRecord(t *testing.T) {
	recordings := []mbRecording{{ID: "studio", Title: "Atlantis", Score: 100, Length: 150000}}
	if got := pickRecording(recordings, "Atlantis", 259000); got == nil {
		t.Fatal("an exact title should match despite the length")
	}
	if got := pickRecording(recordings, "Atlantis Rising Again Tonight", 259000); got != nil {
		t.Fatalf("a loose title should need a close length: %+v", got)
	}
}

func TestRepairTimingFixesLinesThatJumped(t *testing.T) {
	var before, timed []Line
	for i := range 8 {
		start := int64(i) * 5000
		before = append(before, NewLine("one two", start, start+4000))
		timed = append(timed, NewLine("one two", start+30000, start+34000))
	}
	// The intro pulled the first two lines to the start of the song.
	timed[0], timed[1] = NewLine("one two", 500, 600), NewLine("one two", 900, 1000)
	if n := RepairTiming(timed, before); n != 2 {
		t.Fatalf("repaired %d", n)
	}
	if timed[0].StartMs != 30000 || timed[1].StartMs != 35000 || timed[7].StartMs != 65000 {
		t.Fatalf("%d %d %d", timed[0].StartMs, timed[1].StartMs, timed[7].StartMs)
	}
}

func TestRetimeKeepsWordsInProportion(t *testing.T) {
	l := NewLine("one two", 1000, 3000)
	l.Retime(5000, 9000)
	if l.StartMs != 5000 || l.EndMs != 9000 || l.Words[0].StartMs != 5000 || l.Words[len(l.Words)-1].EndMs != 9000 {
		t.Fatalf("%+v", l)
	}
}
