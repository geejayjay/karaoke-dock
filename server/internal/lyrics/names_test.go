package lyrics

import "testing"

func first(c NameClues, known func(string) bool) Guess {
	g := Guesses(c, known)
	if len(g) == 0 {
		return Guess{}
	}
	return g[0]
}

func TestGuessesReadCommonTitles(t *testing.T) {
	cases := []struct {
		name string
		c    NameClues
		want Guess
	}{
		{"singer first", NameClues{RawTitle: "Seafret - Atlantis (Karaoke Version)", Channel: "Sing King"}, Guess{"Seafret", "Atlantis"}},
		{"song first, long title", NameClues{RawTitle: "What If I Miss You For The Rest Of My Life? - Janine Berdin (Karaoke)", Channel: "KaraokeTV"}, Guess{"Janine Berdin", "What If I Miss You For The Rest Of My Life?"}},
		{"pipe", NameClues{RawTitle: "Atlantis Karaoke | Seafret (Piano Karaoke)"}, Guess{"Atlantis", "Seafret"}},
		{"style of", NameClues{RawTitle: "Perfect (In the Style of Ed Sheeran) Karaoke"}, Guess{"Ed Sheeran", "Perfect"}},
		{"made famous by", NameClues{RawTitle: "Hello | Karaoke | Made Famous by Adele"}, Guess{"Adele", "Hello"}},
		{"title by singer", NameClues{RawTitle: "Hello by Adele Karaoke"}, Guess{"Adele", "Hello"}},
		{"channel is the singer", NameClues{RawTitle: "Vulnerable (Official Video)", Channel: "SecondhandSerenadeVEVO"}, Guess{"SecondhandSerenade", "Vulnerable"}},
		{"karaoke channel isn't the singer", NameClues{RawTitle: "Vulnerable", Channel: "Sing King"}, Guess{"", "Vulnerable"}},
		{"music details win", NameClues{RawTitle: "whatever", Track: "Atlantis", Artist: "Seafret"}, Guess{"Seafret", "Atlantis"}},
		{"brand and key pieces dropped", NameClues{RawTitle: "KARAOKE | Seafret - Atlantis | Female Key | KaraokeTV", Channel: "KaraokeTV"}, Guess{"Seafret", "Atlantis"}},
	}
	for _, c := range cases {
		if got := first(c.c, nil); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestKnownSingerDecidesTheOrder(t *testing.T) {
	known := func(s string) bool { return s == "Atlantis" }
	if got := first(NameClues{RawTitle: "Seafret - Atlantis"}, known); got != (Guess{"Atlantis", "Seafret"}) {
		t.Errorf("%+v", got)
	}
}

func TestDatabaseSettlesWhichWayRound(t *testing.T) {
	results := []LrcResult{
		{ArtistName: "Janine Berdin", TrackName: "What if I miss you for the rest of my life? - Live in the Studio", Duration: 222},
		{ArtistName: "Janine Berdin", TrackName: "What if I miss you for the rest of my life?", Duration: 187},
		{ArtistName: "Someone Else", TrackName: "Miss You", Duration: 210},
	}
	guesses := []Guess{{"What If I Miss You For The Rest Of My Life?", "Janine Berdin"}, {"Janine Berdin", "What If I Miss You For The Rest Of My Life?"}}
	got, ok := pickConfirmed(results, guesses, 210000)
	if !ok || got.Artist != "Janine Berdin" || got.Title != "What if I miss you for the rest of my life?" {
		t.Errorf("%v %+v", ok, got)
	}
	if _, ok := pickConfirmed([]LrcResult{{ArtistName: "Nobody", TrackName: "Unrelated Song"}}, guesses, 0); ok {
		t.Error("an unrelated result must not confirm")
	}
}
