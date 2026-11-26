package lyrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanTitle(t *testing.T) {
	cases := map[string]string{
		"Song Name (Official Music Video)":      "Song Name",
		"Song Name [HD] - Lyrics":               "Song Name",
		"Song (Karaoke Version)":                "Song",
		"Love Lyrics Forever (Remastered 2011)": "Love Forever",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSplitArtistTitle(t *testing.T) {
	a, b := SplitArtistTitle("Queen - Bohemian Rhapsody")
	if a != "Queen" || b != "Bohemian Rhapsody" {
		t.Fatal(a, b)
	}
}

func TestParseLRC(t *testing.T) {
	lines := ParseLRC("[ar:x]\n[00:10.50]Hello world\n[00:14.00]\n[00:20.123][00:30.000]Again and again\n")
	if len(lines) != 3 {
		t.Fatalf("%v", lines)
	}
	if lines[0].StartMs != 10500 || lines[0].EndMs != 14000 {
		t.Fatalf("%v", lines[0])
	}
	if lines[1].StartMs != 20123 || lines[2].StartMs != 30000 {
		t.Fatalf("%v", lines)
	}
	w := lines[0].Words
	if len(w) != 2 || w[0].StartMs != 10500 || w[1].EndMs != 14000 || w[0].EndMs != w[1].StartMs {
		t.Fatalf("%+v", w)
	}
}

func TestTokensSplitCJK(t *testing.T) {
	got := tokens("你好 world")
	if strings.Join(got, "|") != "你|好|world" {
		t.Fatal(got)
	}
}

func TestMarkers(t *testing.T) {
	d := Doc{Lines: ParseLRC("[00:01.00](Boy:) one two\n[00:03.00]three\n[00:05.00][Girl] four\n[00:07.00]Both: five\n[00:09.00]M: six")}
	if !d.ApplyMarkers() {
		t.Fatal("no markers")
	}
	wantVoices := []string{"a", "a", "b", "both", "a"}
	for i, v := range wantVoices {
		if d.Lines[i].Voice != v {
			t.Errorf("line %d: voice %q want %q", i, d.Lines[i].Voice, v)
		}
	}
	if d.Lines[0].Text() != "one two" || d.Lines[2].Text() != "four" {
		t.Fatal(d.Lines)
	}
}

func TestPlainLyricsAreNotMarkers(t *testing.T) {
	d := Doc{Lines: ParseLRC("[00:01.00]Mama, just killed a man\n[00:03.00]Friday night")}
	if d.ApplyMarkers() {
		t.Fatal("false marker")
	}
	if d.Lines[0].Voice != "" || d.Lines[0].Text() != "Mama, just killed a man" {
		t.Fatal(d.Lines[0])
	}
}

const ttml = `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:ttm="http://www.w3.org/ns/ttml#metadata" xmlns:amll="http://www.example.com/ns/amll">
<head><metadata><ttm:agent type="person" xml:id="v1"/><amll:meta key="musicName" value="Duet Song"/><amll:meta key="artists" value="Singer One"/></metadata></head>
<body><div>
<p begin="00:01.000" end="00:03.000" ttm:agent="v1"><span begin="00:01.000" end="00:02.000">Hello</span> <span begin="00:02.000" end="00:03.000">there</span><span ttm:role="x-translation">Bonjour</span></p>
<p begin="1:02.500" end="1:04.000" ttm:agent="v2"><span begin="1:02.500" end="1:04.000">Hi</span></p>
</div></body></tt>`

func TestParseTTML(t *testing.T) {
	lines, agents, err := ParseTTML(strings.NewReader(ttml))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || len(agents) != 2 || agents[1] != "v2" {
		t.Fatalf("%v %v", lines, agents)
	}
	if lines[0].Text() != "Hello there" || lines[0].Voice != "v1" {
		t.Fatal(lines[0])
	}
	if lines[1].StartMs != 62500 {
		t.Fatal(lines[1])
	}
}

func TestFindOffset(t *testing.T) {
	env := make([]float64, 2000)
	var starts []int64
	shift := 37 // 3.7 seconds later in the audio than the lyrics say
	for i := 0; i < 20; i++ {
		s := 100 + i*73 + i*i%11
		starts = append(starts, int64(s)*100)
		for j := 0; j < 20; j++ {
			env[s+shift+j] = 1
		}
	}
	got, ok := FindOffset(env, starts, 15000)
	if !ok || got != 3700 {
		t.Fatalf("got %d ok=%v", got, ok)
	}
	if _, ok := FindOffset(make([]float64, 2000), starts, 15000); ok {
		t.Fatal("silence should not match")
	}
}

func TestLRCLIBFindsSongsDespiteGuestsAndLongKaraokeVideos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`[{"trackName":"Uptown Funk (feat. Bruno Mars)","artistName":"Mark Ronson feat. Bruno Mars","duration":270,"syncedLyrics":"[00:01.00]right"},
			{"trackName":"Downtown Blues","artistName":"Mark Ronson","duration":270,"syncedLyrics":"[00:01.00]wrong"}]`))
	}))
	defer srv.Close()
	old := LRCLIBBase
	LRCLIBBase = srv.URL
	defer func() { LRCLIBBase = old }()
	ctx := context.Background()
	r, err := FetchLRCLIB(ctx, "Mark Ronson ft. Bruno Mars", "Uptown Funk", 301)
	if err != nil || r == nil || r.SyncedLyrics != "[00:01.00]right" {
		t.Fatalf("%+v %v", r, err)
	}
	if r, _ := FetchLRCLIB(ctx, "Mark Ronson", "Uptown Funk", 400); r != nil {
		t.Errorf("length too far off: %+v", r)
	}
	if r, _ := FetchLRCLIB(ctx, "Someone Else", "Uptown Funk", 301); r != nil {
		t.Errorf("wrong singer with a long gap: %+v", r)
	}
}

func TestFeaturedSingersAreRemoved(t *testing.T) {
	for in, want := range map[string]string{
		"Mark Ronson ft. Bruno Mars":     "Mark Ronson",
		"Uptown Funk (feat. Bruno Mars)": "Uptown Funk",
		"Simon & Garfunkel":              "Simon & Garfunkel",
		"Soft Cell":                      "Soft Cell",
	} {
		if got := StripFeatured(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestFetchLRCLIBFallsBackToSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]LrcResult{
			{TrackName: "Other", ArtistName: "Nobody", Duration: 200, SyncedLyrics: "[00:01.00]bad"},
			{TrackName: "My Song", ArtistName: "Me", Duration: 181, SyncedLyrics: "[00:01.00]good"},
		})
	}))
	defer srv.Close()
	old := LRCLIBBase
	LRCLIBBase = srv.URL
	defer func() { LRCLIBBase = old }()
	r, err := FetchLRCLIB(context.Background(), "Me", "My Song", 180)
	if err != nil || r == nil || r.SyncedLyrics != "[00:01.00]good" {
		t.Fatalf("%+v %v", r, err)
	}
}
