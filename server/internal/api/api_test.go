package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"karaokedock/internal/admin"
	"karaokedock/internal/jobs"
	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/player"
	"karaokedock/internal/settings"
	"karaokedock/internal/sidecar"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
)

type fixture struct {
	srv  *httptest.Server
	api  *Server
	db   *store.Store
	cfg  *settings.Manager
	room string
	pin  string
	pw   string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg, err := settings.Load(ctx, db, map[string]string{settings.MediaRoot: filepath.Join(dir, "media"), settings.TempRoot: filepath.Join(dir, "tmp")})
	if err != nil {
		t.Fatal(err)
	}
	m := media.NewFs(cfg.Get(settings.MediaRoot))
	sc := sidecar.New(sidecar.Config{})
	enc := &tools.Encoders{}
	amll := lyrics.NewAMLL(filepath.Join(dir, "amll"))
	jm := jobs.New(jobs.Deps{DB: db, Settings: cfg, Media: m, MetadataDir: dir, Sidecar: sc, Encoders: enc, AMLL: amll})
	p := player.New(db, cfg, jm, m, 8080)
	jm.SetNotifier(p)
	ad := admin.New(db, cfg, dir)
	pw, err := ad.ResetPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"tv.html": {Data: []byte("<html>tv</html>")}}
	s := &Server{DB: db, Settings: cfg, Jobs: jm, Player: p, Media: m, Admin: ad, Sidecar: sc, Encoders: enc, AMLL: amll,
		MetadataDir: dir, Web: web, Logs: &LogBuffer{}, Restart: func() {}}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, api: s, db: db, cfg: cfg, room: cfg.Get(settings.RoomCode), pin: cfg.Get(settings.HostPin), pw: pw}
}

func (f *fixture) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+path, nil)
	req.Header.Set("X-Room", f.room)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestEveryListIsCappedAndPaged(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i := range 130 {
		id := fmt.Sprintf("song%07d", i)
		if _, err := f.db.InsertSong(ctx, id, fmt.Sprintf("Title %03d", i)); err != nil {
			t.Fatal(err)
		}
		if err := f.db.SetSongDetails(ctx, id, []store.ArtistInput{{Name: fmt.Sprintf("Singer %03d", i)}}, &store.AlbumInput{Title: fmt.Sprintf("Album %03d", i)}); err != nil {
			t.Fatal(err)
		}
		g, l := fmt.Sprintf("Genre %03d", i), fmt.Sprintf("Lang %03d", i)
		if err := f.db.UpdateSong(ctx, id, store.SongUpdate{Genre: &g, Language: &l}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.AddToQueue(ctx, id, "Sam", "c"); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{"/api/songs", "/api/artists", "/api/albums", "/api/genres", "/api/languages", "/api/queue"} {
		code, body := f.get(t, route)
		if code != 200 || len(body["items"].([]any)) != store.DefaultLimit || body["nextCursor"] == nil {
			t.Errorf("%s default: %d %v", route, code, len(body["items"].([]any)))
		}
		code, body = f.get(t, route+"?limit=100000")
		if code != 200 || len(body["items"].([]any)) != store.MaxLimit {
			t.Errorf("%s capped: %d %v", route, code, len(body["items"].([]any)))
		}
		cursor := body["nextCursor"].(string)
		code, body = f.get(t, route+"?limit=100&cursor="+url.QueryEscape(cursor))
		if code != 200 || len(body["items"].([]any)) != 30 || body["nextCursor"] != nil {
			t.Errorf("%s last page: %d %v %v", route, code, len(body["items"].([]any)), body["nextCursor"])
		}
	}
	if code, _ := f.get(t, "/api/songs?cursor=@@@"); code != 400 {
		t.Errorf("bad cursor: %d", code)
	}
	code, body := f.get(t, "/api/artists?from=m&limit=5")
	if code != 200 || len(body["items"].([]any)) != 5 {
		t.Errorf("from: %d %v", code, body)
	}
}

func TestRoomCodeRequired(t *testing.T) {
	f := setup(t)
	resp, err := http.Get(f.srv.URL + "/api/songs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal(resp.StatusCode)
	}
}

func post(t *testing.T, f *fixture, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Room", f.room)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestHostPinRequired(t *testing.T) {
	f := setup(t)
	if c := post(t, f, "/api/player/skip", "{}", nil).StatusCode; c != 401 {
		t.Fatal(c)
	}
	if c := post(t, f, "/api/player/skip", "{}", map[string]string{"X-Host-Pin": f.pin}).StatusCode; c != 200 {
		t.Fatal(c)
	}
}

func TestAdminLoginAndOriginCheck(t *testing.T) {
	f := setup(t)
	if c := post(t, f, "/api/admin/login", `{"password":"nope"}`, map[string]string{"Origin": f.srv.URL}).StatusCode; c != 401 {
		t.Fatal(c)
	}
	body := fmt.Sprintf(`{"password":%q}`, f.pw)
	if c := post(t, f, "/api/admin/login", body, map[string]string{"Origin": "http://evil.example"}).StatusCode; c != 403 {
		t.Fatal(c)
	}
	resp := post(t, f, "/api/admin/login", body, map[string]string{"Origin": f.srv.URL})
	if resp.StatusCode != 200 || len(resp.Cookies()) == 0 || !resp.Cookies()[0].HttpOnly {
		t.Fatal(resp.StatusCode, resp.Cookies())
	}
	r2, _ := http.Get(f.srv.URL + "/api/admin/settings")
	r2.Body.Close()
	if r2.StatusCode != 401 {
		t.Fatal(r2.StatusCode)
	}
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/admin/settings", nil)
	req.AddCookie(resp.Cookies()[0])
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != 200 {
		t.Fatal(r3.StatusCode)
	}
}

func TestBadVideoIDRejected(t *testing.T) {
	f := setup(t)
	for _, id := range []string{"--exec=calc", "short", "aaaaaaaaaa b"} {
		if c := post(t, f, "/api/songs/import", fmt.Sprintf(`{"videoId":%q}`, id), nil).StatusCode; c != 400 {
			t.Errorf("%q: %d", id, c)
		}
	}
}

func TestMediaPathTraversalRejected(t *testing.T) {
	f := setup(t)
	for _, p := range []string{"/media/..%2F..%2Fsecret/thumb.jpg", "/covers/..%2Fkaraokedock.db"} {
		resp, err := http.Get(f.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestQueuePlaysFirstPlayableSong(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, id := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb"} {
		if _, err := f.db.InsertSong(ctx, id, id); err != nil {
			t.Fatal(err)
		}
	}
	ready := "playable"
	if err := f.db.UpdateSong(ctx, "bbbbbbbbbbb", store.SongUpdate{State: &ready}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb"} {
		if c := post(t, f, "/api/queue", fmt.Sprintf(`{"songId":%q,"singer":"Sam"}`, id), nil).StatusCode; c != 201 {
			t.Fatal(c)
		}
	}
	cur, err := f.db.CurrentItem(ctx)
	if err != nil || cur == nil || cur.Song.ID != "bbbbbbbbbbb" {
		t.Fatalf("%+v %v", cur, err)
	}
}
