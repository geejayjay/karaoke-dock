package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPagingVisitsEveryRowOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const total = 257
	for i := range total {
		// Same sort key for runs of ten rows, to exercise the ID tie-break.
		if _, err := s.InsertSong(ctx, fmt.Sprintf("id%04d", i), fmt.Sprintf("Song %02d", i/10)); err != nil {
			t.Fatal(err)
		}
	}
	for _, sort := range []string{"title", "new", "popular"} {
		seen := map[string]bool{}
		cursor := ""
		pages := 0
		for {
			p, err := s.ListSongs(ctx, SongQuery{Sort: sort, Cursor: cursor, Limit: 30})
			if err != nil {
				t.Fatal(sort, err)
			}
			if len(p.Items) > 30 {
				t.Fatalf("%s: page has %d rows", sort, len(p.Items))
			}
			for _, r := range p.Items {
				if seen[r.ID] {
					t.Fatalf("%s: %s appeared twice", sort, r.ID)
				}
				seen[r.ID] = true
			}
			pages++
			if p.NextCursor == nil {
				break
			}
			cursor = *p.NextCursor
		}
		if len(seen) != total || pages != 9 {
			t.Fatalf("%s: saw %d rows in %d pages", sort, len(seen), pages)
		}
	}
}

func TestLimitIsCapped(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for i := range 150 {
		if _, err := s.InsertSong(ctx, fmt.Sprintf("x%d", i), fmt.Sprintf("T%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.ListSongs(ctx, SongQuery{Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != MaxLimit {
		t.Fatalf("got %d rows", len(p.Items))
	}
}

func TestSearchAndBrowse(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, id := range []string{"a", "b"} {
		if _, err := s.InsertSong(ctx, id, "Hello "+id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSongDetails(ctx, "a", []ArtistInput{{Name: "The Beatles"}, {Name: "Billy Preston"}}, &AlbumInput{Title: "Let It Be", Year: 1970}); err != nil {
		t.Fatal(err)
	}
	p, err := s.ListSongs(ctx, SongQuery{Q: "beat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].ID != "a" || p.Items[0].Artist != "The Beatles, Billy Preston" {
		t.Fatalf("search: %+v", p.Items)
	}
	artists, err := s.ListArtists(ctx, "", "b", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(artists.Items) != 2 || artists.Items[0].Name != "The Beatles" {
		t.Fatalf("artists: %+v", artists.Items)
	}
	bySinger, err := s.ListSongs(ctx, SongQuery{Artist: artists.Items[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(bySinger.Items) != 1 {
		t.Fatalf("by singer: %+v", bySinger.Items)
	}
	if err := s.DeleteSong(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	artists, _ = s.ListArtists(ctx, "", "", "", 0)
	albums, _ := s.ListAlbums(ctx, "", "", "", "", 0)
	if len(artists.Items) != 0 || len(albums.Items) != 0 {
		t.Fatalf("orphans remain: %+v %+v", artists.Items, albums.Items)
	}
}

func TestFuzzySearchForgivesTyposAndFindsLyrics(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.InsertSong(ctx, "aaaaaaaaaaa", "Superheroes"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSongDetails(ctx, "aaaaaaaaaaa", []ArtistInput{{Name: "The Script"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLyricsText(ctx, "aaaaaaaaaaa", "All her life she has seen"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"superheros", "supperheroes", "the scrpit", "her life she"} {
		page, err := s.FuzzySongs(ctx, q, 10)
		if err != nil || len(page.Items) != 1 {
			t.Errorf("%q: %+v %v", q, page, err)
		}
	}
	if page, _ := s.FuzzySongs(ctx, "completely different", 10); len(page.Items) != 0 {
		t.Errorf("unrelated search matched: %+v", page.Items)
	}
}

func TestABlockedJobWaitsUntilItsTime(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnqueueJob(ctx, "aaaaaaaaaaa", "download"); err != nil {
		t.Fatal(err)
	}
	if j, err := s.ClaimJob(ctx, []string{"download"}); err != nil || j == nil || j.Blocked != 0 {
		t.Fatalf("%+v %v", j, err)
	}
	if err := s.RetryJobLater(ctx, "aaaaaaaaaaa", "download", "blocked", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.ClaimJob(ctx, []string{"download"}); j != nil {
		t.Fatalf("claimed before its time: %+v", j)
	}
	if err := s.RetryJobLater(ctx, "aaaaaaaaaaa", "download", "blocked", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	j, err := s.ClaimJob(ctx, []string{"download"})
	if err != nil || j == nil || j.Blocked != 2 || j.Attempts != 1 {
		t.Fatalf("%+v %v", j, err)
	}
	if err := s.EnqueueJob(ctx, "aaaaaaaaaaa", "download"); err != nil { // reprocessing starts afresh
		t.Fatal(err)
	}
	if j, _ := s.ClaimJob(ctx, []string{"download"}); j == nil || j.Blocked != 0 {
		t.Fatalf("%+v", j)
	}
}

func TestQueueOrderAndNextPlayable(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	ids := []string{"s1", "s2", "s3"}
	var items []int64
	for _, id := range ids {
		if _, err := s.InsertSong(ctx, id, id); err != nil {
			t.Fatal(err)
		}
		q, err := s.AddToQueue(ctx, id, "Sam", "c1")
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, q)
	}
	ready := "playable"
	if err := s.UpdateSong(ctx, "s2", SongUpdate{State: &ready}); err != nil {
		t.Fatal(err)
	}
	next, err := s.NextPlayable(ctx, func(st string) bool { return st == "playable" })
	if err != nil || next == nil || next.Song.ID != "s2" {
		t.Fatalf("next: %+v %v", next, err)
	}
	if err := s.MoveQueueItem(ctx, items[2], 0); err != nil {
		t.Fatal(err)
	}
	p, _ := s.ListQueue(ctx, "", 0)
	if p.Items[0].ID != items[2] || p.Items[1].ID != items[0] {
		t.Fatalf("move: %+v", p.Items)
	}
	if err := s.MoveQueueItem(ctx, items[2], items[0]); err != nil {
		t.Fatal(err)
	}
	p, _ = s.ListQueue(ctx, "", 0)
	if p.Items[0].ID != items[0] || p.Items[1].ID != items[2] || p.Items[2].ID != items[1] {
		t.Fatalf("move after: %+v", p.Items)
	}
}

func TestClaimJobOrder(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, id := range []string{"idle", "later", "soon"} {
		if _, err := s.InsertSong(ctx, id, id); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd := func(id string) {
		if _, err := s.AddToQueue(ctx, id, "x", "c"); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("soon")
	mustAdd("later")
	for _, j := range [][2]string{{"idle", "separate"}, {"later", "separate"}, {"soon", "align"}} {
		if err := s.EnqueueJob(ctx, j[0], j[1]); err != nil {
			t.Fatal(err)
		}
	}
	want := [][2]string{{"later", "separate"}, {"soon", "align"}, {"idle", "separate"}}
	for _, w := range want {
		j, err := s.ClaimJob(ctx, []string{"separate", "align"})
		if err != nil || j == nil {
			t.Fatal(j, err)
		}
		if j.SongID != w[0] || j.Stage != w[1] {
			t.Fatalf("got %s/%s want %v", j.SongID, j.Stage, w)
		}
	}
}

func TestMetadataUpdatesAndCovers(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.InsertSong(ctx, "s1", "First Song"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertSong(ctx, "s2", "Unidentified Song"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSongDetails(ctx, "s1", []ArtistInput{{MBID: "ar1", Name: "Old Singer", Gender: "male"}}, &AlbumInput{ID: "al1", Title: "Old Album", Year: 1999}); err != nil {
		t.Fatal(err)
	}

	unid, err := s.ListSongs(ctx, SongQuery{Unidentified: true})
	if err != nil || len(unid.Items) != 1 || unid.Items[0].ID != "s2" {
		t.Fatalf("unidentified: %+v %v", unid, err)
	}

	if err := s.UpdateArtist(ctx, "ar1", "New Singer", "female"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetArtistCover(ctx, "ar1", "covers/artist-ar1.jpg"); err != nil {
		t.Fatal(err)
	}
	ar, err := s.GetArtist(ctx, "ar1")
	if err != nil || ar.Name != "New Singer" || ar.Gender != "female" || !ar.HasCustom || ar.CoverID != "artist-ar1" || ar.SongCount != 1 {
		t.Fatalf("artist: %+v %v", ar, err)
	}

	if err := s.UpdateAlbum(ctx, "al1", "New Album", 2001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAlbumCover(ctx, "al1", "covers/al1.jpg", "#ff0055"); err != nil {
		t.Fatal(err)
	}
	al, err := s.GetAlbum(ctx, "al1")
	if err != nil || al.Title != "New Album" || al.Year != 2001 || !al.HasCover || al.SongCount != 1 {
		t.Fatalf("album: %+v %v", al, err)
	}

	song, err := s.GetSong(ctx, "s1")
	if err != nil || song.Artist != "New Singer" || song.Album != "New Album" || !song.HasThumb || song.AccentColor != "#ff0055" {
		t.Fatalf("song: %+v %v", song, err)
	}
}
