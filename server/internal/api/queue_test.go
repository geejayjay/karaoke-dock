package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"karaokedock/internal/store"
	"karaokedock/internal/tools"
)

func (f *fixture) as(t *testing.T, client, method, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Room", f.room)
	req.AddCookie(&http.Cookie{Name: clientCookie, Value: client})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (f *fixture) song(t *testing.T, id, state string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.db.InsertSong(ctx, id, "Title "+id); err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateSong(ctx, id, store.SongUpdate{State: &state}); err != nil {
		t.Fatal(err)
	}
}

func TestGuestCanRemoveOwnSong(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	const owner, stranger = "owner-client-0001", "stranger-client-1"
	f.song(t, "aaaaaaaaaaa", "playable")
	f.song(t, "bbbbbbbbbbb", "playable")
	f.song(t, "ccccccccccc", "playable")
	for _, id := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"} {
		if code := f.as(t, owner, "POST", "/api/queue", fmt.Sprintf(`{"songId":%q,"singer":"Sam"}`, id)); code != 201 {
			t.Fatalf("add %s: %d", id, code)
		}
	}
	playing, err := f.db.CurrentItem(ctx)
	if err != nil || playing == nil || playing.Song.ID != "aaaaaaaaaaa" {
		t.Fatalf("first song should play: %v %v", playing, err)
	}
	if f.api.Player.State().Current.Owner != store.ClientTag(owner) {
		t.Error("playing song should name its owner")
	}

	// A waiting song, then the song that's on screen.
	waiting := fmt.Sprintf("/api/queue/%d", playing.ID+2)
	if code := f.as(t, stranger, "DELETE", waiting, ""); code != 403 {
		t.Errorf("stranger removing a waiting song: %d", code)
	}
	if code := f.as(t, owner, "DELETE", waiting, ""); code != 200 {
		t.Errorf("owner removing a waiting song: %d", code)
	}
	now := fmt.Sprintf("/api/queue/%d", playing.ID)
	if code := f.as(t, stranger, "DELETE", now, ""); code != 403 {
		t.Errorf("stranger removing the playing song: %d", code)
	}
	if cur, _ := f.db.CurrentItem(ctx); cur == nil || cur.ID != playing.ID {
		t.Error("a refused remove must not stop the song")
	}
	if code := f.as(t, owner, "DELETE", now, ""); code != 200 {
		t.Errorf("owner removing the playing song: %d", code)
	}
	if cur, _ := f.db.CurrentItem(ctx); cur == nil || cur.Song.ID != "bbbbbbbbbbb" {
		t.Errorf("next song should start: %+v", cur)
	}
}

func TestASongCanOnlyBeQueuedOnce(t *testing.T) {
	f := setup(t)
	f.song(t, "aaaaaaaaaaa", "playable")
	add := `{"songId":"aaaaaaaaaaa","singer":"Sam"}`
	if code := f.as(t, "first-client-00001", "POST", "/api/queue", add); code != 201 {
		t.Fatalf("first add: %d", code)
	}
	if code := f.as(t, "other-client-00002", "POST", "/api/queue", add); code != 409 {
		t.Errorf("duplicate add: %d", code)
	}
	code, body := f.get(t, "/api/queue/songs")
	if ids, _ := body["ids"].([]any); code != 200 || len(ids) != 1 || ids[0] != "aaaaaaaaaaa" {
		t.Errorf("%d %v", code, body)
	}
}

func TestGuestSeesTheirPlaceAndHistory(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.song(t, "aaaaaaaaaaa", "queued")
	f.song(t, "bbbbbbbbbbb", "queued")
	f.song(t, "ccccccccccc", "queued")
	if _, err := f.db.AddToQueue(ctx, "aaaaaaaaaaa", "Ann", "someone-else-00001"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddToQueue(ctx, "bbbbbbbbbbb", "Sam", "me-client-00000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddToQueue(ctx, "ccccccccccc", "Sam", "me-client-00000001"); err != nil {
		t.Fatal(err)
	}
	item, ahead, err := f.db.NextForClient(ctx, "me-client-00000001")
	if err != nil || item == nil || item.Song.ID != "bbbbbbbbbbb" || ahead != 1 {
		t.Errorf("%+v %d %v", item, ahead, err)
	}
	page, err := f.db.SungBefore(ctx, "me-client-00000001", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "ccccccccccc" || page.NextCursor == nil {
		t.Fatalf("%+v %v", page, err)
	}
	page, err = f.db.SungBefore(ctx, "me-client-00000001", *page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "bbbbbbbbbbb" || page.NextCursor != nil {
		t.Errorf("%+v %v", page, err)
	}
}

func TestProcessingGroups(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	audio, yes := "audio/key", true
	f.song(t, "processing1", "separating")
	f.song(t, "lyricsToGo1", "aligning")
	f.song(t, "processed01", "complete")
	f.song(t, "noLyrics001", "complete")
	f.song(t, "failed00001", "failed")
	for id, u := range map[string]store.SongUpdate{
		"lyricsToGo1": {AudioKey: &audio, HasLyrics: &yes},
		"processed01": {AudioKey: &audio, HasLyrics: &yes},
		"noLyrics001": {AudioKey: &audio},
	} {
		if err := f.db.UpdateSong(ctx, id, u); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][]string{
		store.FilterWorking: {"lyricsToGo1", "processing1"},
		store.FilterDone:    {"processed01"},
		store.FilterProblem: {"failed00001", "noLyrics001"},
	}
	for filter, ids := range want {
		page, err := f.db.ListProcessing(ctx, filter, "", 0)
		if err != nil {
			t.Fatal(filter, err)
		}
		got := map[string]bool{}
		for i := range page.Items {
			got[page.Items[i].ID] = true
			if st := f.api.Jobs.Status(&page.Items[i]); st.Overall != filter {
				t.Errorf("%s is listed as %s but its status says %s", page.Items[i].ID, filter, st.Overall)
			}
		}
		if len(got) != len(ids) {
			t.Errorf("%s: got %v, want %v", filter, got, ids)
		}
		for _, id := range ids {
			if !got[id] {
				t.Errorf("%s: missing %s", filter, id)
			}
		}
	}
	c, err := f.db.CountProcessing(ctx)
	if err != nil || c != (store.ProcessingCounts{All: 5, Working: 2, Done: 1, Problem: 2}) {
		t.Errorf("counts: %+v %v", c, err)
	}
	if _, err := f.db.ListProcessing(ctx, "nonsense", "", 0); err == nil {
		t.Error("unknown filter should be refused")
	}
}

func TestOwnedSongsUseOurDetails(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.song(t, "owned000001", "complete")
	f.song(t, "failed00001", "failed")
	if err := f.db.SetSongDetails(ctx, "owned000001", []store.ArtistInput{{Name: "Our Singer"}}, nil); err != nil {
		t.Fatal(err)
	}
	rows := f.api.withOwned(ctx, []tools.SearchResult{
		{VideoID: "owned000001", Title: "OFFICIAL VIDEO (4K)", Channel: "SomeChannelVEVO"},
		{VideoID: "failed00001", Title: "From YouTube"},
		{VideoID: "newvideo001", Title: "New from YouTube"},
	})
	if r := rows[0]; r.Song == nil || r.Title != "Title owned000001" || r.Channel != "Our Singer" {
		t.Errorf("owned song kept YouTube's details: %+v", r)
	}
	if rows[1].Song != nil || rows[1].Title != "From YouTube" {
		t.Errorf("failed song should stay a YouTube result: %+v", rows[1])
	}
	if rows[2].Song != nil || rows[2].Title != "New from YouTube" {
		t.Errorf("new song changed: %+v", rows[2])
	}
}
