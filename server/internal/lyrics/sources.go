package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const userAgent = "KaraokeDock/0.1 (self-hosted karaoke app)"

var httpClient = &http.Client{Timeout: 20 * time.Second}

func getJSON(ctx context.Context, u string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s: status %d", u, resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// LrcResult is one LRCLIB record.
type LrcResult struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

// LRCLIBBase can be replaced in tests.
var LRCLIBBase = "https://lrclib.net"

const (
	lrcCloseSec = 15 // lengths this close count as the same recording
	lrcLooseSec = 45 // karaoke videos add long intros, so a sure name match can differ this much
)

var featured = regexp.MustCompile(`(?i)\s*[\(\[]?\s*\b(?:ft|feat|featuring)\b\.?\s+.*$`)

// StripFeatured removes guest singers from a name, as in "Uptown Funk (feat. Bruno Mars)".
func StripFeatured(name string) string {
	if out := featured.ReplaceAllString(name, ""); out != "" {
		return out
	}
	return name
}

// FetchLRCLIB tries an exact lookup and then a search. It returns nil when
// nothing usable exists.
func FetchLRCLIB(ctx context.Context, artist, title string, durationSec float64) (*LrcResult, error) {
	artist, title = StripFeatured(artist), StripFeatured(title)
	if artist != "" && durationSec > 0 {
		q := url.Values{"artist_name": {artist}, "track_name": {title}, "duration": {fmt.Sprintf("%.0f", durationSec)}}
		var r LrcResult
		code, err := getJSON(ctx, LRCLIBBase+"/api/get?"+q.Encode(), &r)
		if err == nil && r.SyncedLyrics != "" {
			return &r, nil
		}
		if err != nil && code != http.StatusNotFound && code != 0 && code < 500 {
			return nil, err
		}
	}
	var results []LrcResult
	search := strings.TrimSpace(artist + " " + title)
	if _, err := getJSON(ctx, LRCLIBBase+"/api/search?"+url.Values{"q": {search}}.Encode(), &results); err != nil {
		return nil, err
	}
	var best *LrcResult
	bestScore := 0.0
	for i := range results {
		r := &results[i]
		if r.SyncedLyrics == "" {
			continue
		}
		track, singer := Similarity(StripFeatured(r.TrackName), title), Similarity(StripFeatured(r.ArtistName), artist)
		s := track + 0.5*singer
		if durationSec > 0 {
			diff := math.Abs(r.Duration - durationSec)
			switch {
			case diff <= lrcCloseSec:
				s += 1 - diff/lrcCloseSec
			case diff > lrcLooseSec || track < 0.9 || singer < 0.5:
				continue
			}
		}
		if s > bestScore {
			best, bestScore = r, s
		}
	}
	if bestScore < 1.0 {
		return nil, nil
	}
	return best, nil
}

// MusicBrainz access: at most one request per second.
var (
	mbMu   sync.Mutex
	mbLast time.Time

	// MBBase can be replaced in tests.
	MBBase = "https://musicbrainz.org/ws/2"
	// CoverBase can be replaced in tests.
	CoverBase = "https://coverartarchive.org"
)

func mbGet(ctx context.Context, path string, q url.Values, out any) error {
	mbMu.Lock()
	if wait := time.Until(mbLast.Add(1100 * time.Millisecond)); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			mbMu.Unlock()
			return ctx.Err()
		}
	}
	mbLast = time.Now()
	mbMu.Unlock()
	q.Set("fmt", "json")
	u := MBBase + path + "?" + q.Encode()
	code, err := getJSON(ctx, u, out)
	// MusicBrainz answers 503 when it's busy, so wait and try again.
	for wait := 2 * time.Second; code == http.StatusServiceUnavailable && wait <= 4*time.Second; wait *= 2 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		code, err = getJSON(ctx, u, out)
	}
	return err
}

// Details is what MusicBrainz knows about a recording.
type Details struct {
	RecordingID    string   `json:"recordingId,omitempty"`
	Title          string   `json:"title"`
	Artists        []Artist `json:"artists"`
	Album          string   `json:"album,omitempty"`
	AlbumMBID      string   `json:"albumMbid,omitempty"`
	ReleaseGroupID string   `json:"releaseGroupId,omitempty"`
	Year           int      `json:"year,omitempty"`
	Genre          string   `json:"genre,omitempty"`
	Language       string   `json:"language,omitempty"`
}

// Artist is a credited performer.
type Artist struct {
	MBID   string `json:"mbid,omitempty"`
	Name   string `json:"name"`
	Gender string `json:"gender,omitempty"`
}

type mbArtistCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

type mbRelease struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Date   string `json:"date"`
	Text   struct {
		Language string `json:"language"`
	} `json:"text-representation"`
	ReleaseGroup struct {
		ID          string `json:"id"`
		PrimaryType string `json:"primary-type"`
	} `json:"release-group"`
}

type mbRecording struct {
	ID       string           `json:"id"`
	Title    string           `json:"title"`
	Score    int              `json:"score"`
	Length   int64            `json:"length"`
	Credits  []mbArtistCredit `json:"artist-credit"`
	Releases []mbRelease      `json:"releases"`
	Genres   []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"genres"`
}

func luceneEscape(s string) string {
	return regexp.MustCompile(`([+\-!(){}\[\]^"~*?:\\/&|])`).ReplaceAllString(s, `\$1`)
}

// LookupDetails finds a recording and its earliest official album.
func LookupDetails(ctx context.Context, artist, title string, durationMs int64) (*Details, error) {
	return lookupDetails(ctx, artist, title, durationMs)
}

var variantTitle = regexp.MustCompile(`(?i)\b(slowed|sped[\s-]*up|reverb|nightcore|remix|live|acoustic|instrumental|karaoke|demo|cover|version|edit)\b`)

// pickRecording chooses the best match. It skips remixes and other versions the
// search didn't ask for. A music video can run much longer than the record, so
// length only rules out a match when the title isn't exact.
func pickRecording(recordings []mbRecording, title string, durationMs int64) *mbRecording {
	var pick *mbRecording
	bestScore := 0.0
	wantVariant := variantTitle.MatchString(title)
	for i := range recordings {
		r := &recordings[i]
		if r.Score < 80 || (!wantVariant && variantTitle.MatchString(r.Title)) {
			continue
		}
		name := Similarity(r.Title, title)
		s := name
		if durationMs > 0 && r.Length > 0 {
			diff := math.Abs(float64(r.Length-durationMs)) / 1000
			if name >= 0.9 {
				s += max(0, 1-diff/90)
			} else if diff > 30 {
				continue
			} else {
				s += 1 - diff/30
			}
		}
		if hasOfficialAlbum(r) { // has the details and art the song will show
			s += 0.5
		}
		if s > bestScore {
			pick, bestScore = r, s
		}
	}
	return pick
}

// earliestRelease finds the first official release of a type, or of any type.
func earliestRelease(releases []mbRelease, primaryType string) *mbRelease {
	var found *mbRelease
	for i := range releases {
		r := &releases[i]
		if r.Status != "Official" || r.Date == "" || (primaryType != "" && r.ReleaseGroup.PrimaryType != primaryType) {
			continue
		}
		if found == nil || r.Date < found.Date {
			found = r
		}
	}
	return found
}

func hasOfficialAlbum(r *mbRecording) bool {
	return earliestRelease(r.Releases, "Album") != nil
}

func lookupDetails(ctx context.Context, artist, title string, durationMs int64) (*Details, error) {
	artist, title = StripFeatured(artist), StripFeatured(title)
	query := `recording:"` + luceneEscape(title) + `"`
	if artist != "" {
		query += ` AND artist:"` + luceneEscape(artist) + `"`
	}
	var found struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if err := mbGet(ctx, "/recording", url.Values{"query": {query}, "limit": {"10"}}, &found); err != nil {
		return nil, err
	}
	pick := pickRecording(found.Recordings, title, durationMs)
	if pick == nil {
		return nil, nil
	}
	return LookupRecording(ctx, pick.ID, "")
}

// LookupRecording fetches full details, genres and artist genders for one recording ID.
func LookupRecording(ctx context.Context, recordingID, releaseGroupID string) (*Details, error) {
	var full mbRecording
	if err := mbGet(ctx, "/recording/"+recordingID, url.Values{"inc": {"artist-credits genres releases release-groups"}}, &full); err != nil {
		return nil, err
	}
	d := &Details{RecordingID: full.ID, Title: full.Title}
	for _, c := range full.Credits {
		a := Artist{MBID: c.Artist.ID, Name: c.Artist.Name}
		var info struct {
			Gender string `json:"gender"`
		}
		if err := mbGet(ctx, "/artist/"+a.MBID, url.Values{}, &info); err == nil {
			a.Gender = strings.ToLower(info.Gender)
		}
		d.Artists = append(d.Artists, a)
	}
	genres := full.Genres
	sort.SliceStable(genres, func(i, j int) bool { return genres[i].Count > genres[j].Count })
	if len(genres) > 0 {
		d.Genre = titleCase(genres[0].Name)
	}
	var album *mbRelease
	if releaseGroupID != "" {
		for i := range full.Releases {
			if full.Releases[i].ReleaseGroup.ID == releaseGroupID {
				album = &full.Releases[i]
				break
			}
		}
	}
	if album == nil {
		album = earliestRelease(full.Releases, "Album")
	}
	if album == nil { // an EP or single still has art
		album = earliestRelease(full.Releases, "")
	}
	for i := range full.Releases { // language from any release
		if l := LanguageCode(full.Releases[i].Text.Language); l != "" {
			d.Language = l
			break
		}
	}
	if album != nil {
		d.Album, d.AlbumMBID, d.ReleaseGroupID = album.Title, album.ID, album.ReleaseGroup.ID
		fmt.Sscanf(album.Date, "%d", &d.Year)
	}
	return d, nil
}

// SearchRecordings finds candidate recordings from MusicBrainz for re-identifying a song.
func SearchRecordings(ctx context.Context, artist, title string) ([]Details, error) {
	artist, title = strings.TrimSpace(StripFeatured(artist)), strings.TrimSpace(StripFeatured(title))
	if title == "" && artist == "" {
		return nil, nil
	}
	var query string
	if title != "" && artist != "" {
		query = `recording:"` + luceneEscape(title) + `" AND artist:"` + luceneEscape(artist) + `"`
	} else if title != "" {
		query = `recording:"` + luceneEscape(title) + `"`
	} else {
		query = `artist:"` + luceneEscape(artist) + `"`
	}
	var found struct {
		Recordings []mbRecording `json:"recordings"`
	}
	if err := mbGet(ctx, "/recording", url.Values{"query": {query}, "limit": {"15"}}, &found); err != nil {
		return nil, err
	}
	if len(found.Recordings) == 0 && title != "" && artist != "" {
		loose := luceneEscape(title) + " AND " + luceneEscape(artist)
		if err := mbGet(ctx, "/recording", url.Values{"query": {loose}, "limit": {"15"}}, &found); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	out := make([]Details, 0, 8)
	for i := range found.Recordings {
		r := &found.Recordings[i]
		var artists []Artist
		var artistNames []string
		for _, c := range r.Credits {
			name := c.Artist.Name
			if name == "" {
				name = c.Name
			}
			if name != "" {
				artists = append(artists, Artist{MBID: c.Artist.ID, Name: name})
				artistNames = append(artistNames, strings.ToLower(name))
			}
		}
		album := earliestRelease(r.Releases, "Album")
		if album == nil {
			album = earliestRelease(r.Releases, "")
		}
		if album == nil && len(r.Releases) > 0 {
			album = &r.Releases[0]
		}
		d := Details{RecordingID: r.ID, Title: r.Title, Artists: artists}
		if album != nil {
			d.Album, d.AlbumMBID, d.ReleaseGroupID = album.Title, album.ID, album.ReleaseGroup.ID
			fmt.Sscanf(album.Date, "%d", &d.Year)
		}
		for j := range r.Releases {
			if l := LanguageCode(r.Releases[j].Text.Language); l != "" {
				d.Language = LanguageName(l)
				break
			}
		}
		key := strings.ToLower(d.Title) + "|" + strings.Join(artistNames, ",") + "|" + strings.ToLower(d.Album)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
		if len(out) >= 8 {
			break
		}
	}
	return out, nil
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// DownloadCover saves a release group's front cover. It returns false when
// there is none.
func DownloadCover(ctx context.Context, releaseGroupID, path string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CoverBase+"/release-group/"+releaseGroupID+"/front-250", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("cover: status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return false, err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 4<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// AMLL is a local index of per-line duet lyrics.
type AMLL struct {
	dir  string
	once sync.Once
	idx  map[string][]amllEntry
}

type amllEntry struct {
	artists string
	path    string
}

// NewAMLL points at a local clone. A missing folder simply finds nothing.
func NewAMLL(dir string) *AMLL { return &AMLL{dir: dir} }

var amllMeta = regexp.MustCompile(`key="(musicName|artists)"\s+value="([^"]*)"`)

func normKey(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == '-' || r == '\'' }), "")
}

func (a *AMLL) build() {
	a.idx = map[string][]amllEntry{}
	_ = filepath.WalkDir(a.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "raw-lyrics" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".ttml") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil //nolint:nilerr
		}
		defer f.Close()
		head := make([]byte, 32*1024)
		n, _ := io.ReadFull(f, head)
		var names, artists []string
		for _, m := range amllMeta.FindAllSubmatch(head[:n], -1) {
			if string(m[1]) == "musicName" {
				names = append(names, string(m[2]))
			} else {
				artists = append(artists, string(m[2]))
			}
		}
		for _, name := range names {
			k := normKey(name)
			a.idx[k] = append(a.idx[k], amllEntry{strings.Join(artists, " "), p})
		}
		return nil
	})
}

// Rebuild forgets the index, after updating the clone.
func (a *AMLL) Rebuild() { a.once = sync.Once{} }

// Find returns a song's per-line lyrics, or nil when not indexed.
func (a *AMLL) Find(title, artist string) (lines []Line, agents []string, err error) {
	a.once.Do(a.build)
	for _, e := range a.idx[normKey(title)] {
		if artist != "" && Similarity(e.artists, artist) < 0.3 {
			continue
		}
		f, err := os.Open(e.path)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		lines, agents, err = ParseTTML(f)
		if err != nil {
			return nil, nil, errors.Join(errors.New("amll parse"), err)
		}
		return lines, agents, nil
	}
	return nil, nil, nil
}
