package store

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Page limits for every list route.
const (
	DefaultLimit = 30
	MaxLimit     = 100
)

// SinglesAlbum selects songs that have no album.
const SinglesAlbum = "__singles"

// ErrBadCursor means the client sent a cursor this server cannot read.
var ErrBadCursor = errors.New("bad cursor")

// ErrBadFilter means the client asked for a list group that doesn't exist.
var ErrBadFilter = errors.New("bad filter")

// ClampLimit applies the default and the maximum.
func ClampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return min(n, MaxLimit)
}

// EncodeCursor packs a sort value and row ID into an opaque string.
func EncodeCursor(sortValue, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sortValue + "\x00" + id))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(c string) (sortValue, id string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", ErrBadCursor
	}
	sortValue, id, ok := strings.Cut(string(b), "\x00")
	if !ok {
		return "", "", ErrBadCursor
	}
	return sortValue, id, nil
}

// SortKey makes the A-Z ordering key: lower case, no leading article or
// punctuation.
func SortKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimLeftFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, a := range []string{"the ", "a ", "an "} {
		if t, ok := strings.CutPrefix(s, a); ok && t != "" {
			return t
		}
	}
	return s
}

// HashID derives a stable ID from text.
func HashID(parts ...string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.Join(parts, "\x00"))))
	return hex.EncodeToString(h[:8])
}

// Song is a full library row.
type Song struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	AlbumID        string `json:"albumId"`
	Album          string `json:"album"`
	Genre          string `json:"genre"`
	Language       string `json:"language"`
	DurationMs     int64  `json:"durationMs"`
	State          string `json:"state"`
	Error          string `json:"error,omitempty"`
	HasLyrics      bool   `json:"hasLyrics"`
	KaraokeVersion bool   `json:"karaokeVersion"`
	AudioKey       string `json:"-"`
	LyricsKey      string `json:"-"`
	VideoKey       string `json:"-"`
	GuideKey       string `json:"-"`
	AccentColor    string `json:"accentColor"`
	HasThumb       bool   `json:"hasThumb"`
	HasBg          bool   `json:"hasBg"`
	PlayCount      int64  `json:"playCount"`
	CreatedAt      int64  `json:"createdAt"`
	// Silence cut from the saved audio. Lyrics stay timed to the uncut song.
	TrimStartMs int64 `json:"trimStartMs"`
	TrimEndMs   int64 `json:"trimEndMs"`
	// How many lines were timed exactly from the video, out of how many.
	TimedLines int64 `json:"timedLines"`
	LyricLines int64 `json:"lyricLines"`
}

// SongRow is the small shape used in lists.
type SongRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album,omitempty"`
	DurationMs int64  `json:"durationMs"`
	State      string `json:"state"`
	HasLyrics  bool   `json:"hasLyrics"`
	Karaoke    bool   `json:"karaoke,omitempty"`
	CoverID    string `json:"coverId,omitempty"` // the album whose art to show
}

// coverIDSQL selects a song's album when that album has art.
const coverIDSQL = `CASE WHEN s.has_thumb=1 THEN s.album_id ELSE '' END`

const songCols = `id,title,artist_display,album_id,album_title,genre,language,duration_ms,state,error,
 has_lyrics,karaoke_version,audio_key,lyrics_key,video_key,guide_key,accent_color,has_thumb,has_bg,play_count,created_at,trim_start_ms,trim_end_ms,timed_lines,lyric_lines`

func scanSong(r interface{ Scan(...any) error }) (*Song, error) {
	var s Song
	err := r.Scan(&s.ID, &s.Title, &s.Artist, &s.AlbumID, &s.Album, &s.Genre, &s.Language, &s.DurationMs,
		&s.State, &s.Error, &s.HasLyrics, &s.KaraokeVersion, &s.AudioKey, &s.LyricsKey, &s.VideoKey, &s.GuideKey,
		&s.AccentColor, &s.HasThumb, &s.HasBg, &s.PlayCount, &s.CreatedAt, &s.TrimStartMs, &s.TrimEndMs, &s.TimedLines, &s.LyricLines)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSong returns one song, or sql.ErrNoRows.
func (s *Store) GetSong(ctx context.Context, id string) (*Song, error) {
	return scanSong(s.R.QueryRowContext(ctx, `SELECT `+songCols+` FROM songs WHERE id=?`, id))
}

// InsertSong adds a new song row if none exists, and reports whether it did.
func (s *Store) InsertSong(ctx context.Context, id, title string) (bool, error) {
	res, err := s.W.ExecContext(ctx, `INSERT OR IGNORE INTO songs(id,title,title_sort,created_at) VALUES(?,?,?,?)`,
		id, title, SortKey(title), time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, err = s.W.ExecContext(ctx, `INSERT INTO songs_fts(id,title,artists,album) VALUES(?,?,'','')`, id, title)
	}
	return n > 0, err
}

// SongUpdate lists the columns a job or admin may change. Nil means unchanged.
type SongUpdate struct {
	Title, Artist, Genre, Language, State, Error         *string
	AudioKey, LyricsKey, VideoKey, GuideKey, AccentColor *string
	DurationMs, TrimStartMs, TrimEndMs                   *int64
	TimedLines, LyricLines                               *int64
	HasLyrics, KaraokeVersion, HasThumb, HasBg           *bool
}

// UpdateSong applies the non-nil fields.
func (s *Store) UpdateSong(ctx context.Context, id string, u SongUpdate) error {
	var sets []string
	var args []any
	add := func(col string, v any) {
		sets = append(sets, col+"=?")
		args = append(args, v)
	}
	str := map[string]*string{
		"artist_display": u.Artist, "genre": u.Genre, "language": u.Language, "state": u.State, "error": u.Error,
		"audio_key": u.AudioKey, "lyrics_key": u.LyricsKey, "video_key": u.VideoKey, "guide_key": u.GuideKey,
		"accent_color": u.AccentColor,
	}
	for col, p := range str {
		if p != nil {
			add(col, *p)
		}
	}
	if u.Title != nil {
		add("title", *u.Title)
		add("title_sort", SortKey(*u.Title))
	}
	if u.DurationMs != nil {
		add("duration_ms", *u.DurationMs)
	}
	if u.TrimStartMs != nil {
		add("trim_start_ms", *u.TrimStartMs)
	}
	if u.TrimEndMs != nil {
		add("trim_end_ms", *u.TrimEndMs)
	}
	if u.TimedLines != nil {
		add("timed_lines", *u.TimedLines)
	}
	if u.LyricLines != nil {
		add("lyric_lines", *u.LyricLines)
	}
	for col, p := range map[string]*bool{"has_lyrics": u.HasLyrics, "karaoke_version": u.KaraokeVersion, "has_thumb": u.HasThumb, "has_bg": u.HasBg} {
		if p != nil {
			add(col, *p)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	if _, err := s.W.ExecContext(ctx, `UPDATE songs SET `+strings.Join(sets, ",")+` WHERE id=?`, args...); err != nil {
		return err
	}
	if u.Title != nil || u.Artist != nil {
		return s.refreshFTS(ctx, id)
	}
	return nil
}

func (s *Store) refreshFTS(ctx context.Context, id string) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM songs_fts WHERE id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.W.ExecContext(ctx, `INSERT INTO songs_fts(id,title,artists,album)
		SELECT id,title,artist_display,album_title FROM songs WHERE id=?`, id)
	return err
}

// ArtistInput names one singer of a song.
type ArtistInput struct {
	MBID, Name, Gender string
}

// AlbumInput names the song's album.
type AlbumInput struct {
	ID, Title, CoverKey string
	Year                int
}

// SetSongDetails replaces the singers and album of a song.
func (s *Store) SetSongDetails(ctx context.Context, songID string, artists []ArtistInput, album *AlbumInput) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM song_artists WHERE song_id=?`, songID); err != nil {
		return err
	}
	var names []string
	firstID := ""
	for i, a := range artists {
		id := a.MBID
		if id == "" {
			id = HashID(a.Name)
		}
		if i == 0 {
			firstID = id
		}
		names = append(names, a.Name)
		if _, err := tx.ExecContext(ctx, `INSERT INTO artists(id,name,name_sort,gender) VALUES(?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, name_sort=excluded.name_sort,
			gender=CASE WHEN excluded.gender<>'' THEN excluded.gender ELSE gender END`,
			id, a.Name, SortKey(a.Name), a.Gender); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO song_artists(song_id,artist_id,pos) VALUES(?,?,?)`, songID, id, i); err != nil {
			return err
		}
	}
	albumID, albumTitle := "", ""
	if album != nil && album.Title != "" {
		albumID, albumTitle = album.ID, album.Title
		if albumID == "" {
			albumID = HashID(firstID, album.Title)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO albums(id,title,title_sort,artist_id,year,cover_key) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET title=excluded.title, title_sort=excluded.title_sort,
			artist_id=excluded.artist_id, year=CASE WHEN excluded.year<>0 THEN excluded.year ELSE year END,
			cover_key=CASE WHEN excluded.cover_key<>'' THEN excluded.cover_key ELSE cover_key END`,
			albumID, album.Title, SortKey(album.Title), firstID, album.Year, album.CoverKey); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE songs SET artist_display=?, album_id=?, album_title=? WHERE id=?`,
		strings.Join(names, ", "), albumID, albumTitle, songID); err != nil {
		return err
	}
	if err := pruneOrphans(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM songs_fts WHERE id=?`, songID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO songs_fts(id,title,artists,album)
		SELECT id,title,artist_display,album_title FROM songs WHERE id=?`, songID); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneOrphans(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artists WHERE id NOT IN (SELECT artist_id FROM song_artists)`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM albums WHERE id NOT IN (SELECT album_id FROM songs WHERE album_id<>'')`)
	return err
}

// DeleteSong removes a song and everything indexing it.
func (s *Store) DeleteSong(ctx context.Context, id string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM songs WHERE id=?`, `DELETE FROM songs_fts WHERE id=?`, `DELETE FROM song_artists WHERE song_id=?`,
		`DELETE FROM jobs WHERE song_id=?`, `DELETE FROM queue_items WHERE song_id=? AND status='waiting'`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if err := pruneOrphans(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// IncrementPlayCount records one finished play.
func (s *Store) IncrementPlayCount(ctx context.Context, id string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE songs SET play_count=play_count+1 WHERE id=?`, id)
	return err
}

// SongQuery selects a page of songs.
type SongQuery struct {
	Q, Artist, Album, Genre, Language string
	Sort, From, Cursor                string
	Limit                             int
	OnlyMissing                       bool
	Unidentified                      bool
}

// Page is the response shape for every list.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// ftsQuery turns free text into safe prefix terms.
func ftsQuery(q string) string {
	var terms []string
	for _, f := range strings.Fields(q) {
		f = strings.ReplaceAll(f, `"`, "")
		if f != "" {
			terms = append(terms, `"`+f+`"*`)
		}
	}
	return strings.Join(terms, " ")
}

// ListSongs returns one page, ordered by the keyset for the chosen sort.
func (s *Store) ListSongs(ctx context.Context, q SongQuery) (*Page[SongRow], error) {
	limit := ClampLimit(q.Limit)
	col, dir, numeric := "s.title_sort", "ASC", false
	switch q.Sort {
	case "new":
		col, dir, numeric = "s.created_at", "DESC", true
	case "popular":
		col, dir, numeric = "s.play_count", "DESC", true
	case "", "title":
	default:
		return nil, fmt.Errorf("unknown sort %q", q.Sort)
	}
	idCol := "s.id"
	var where []string
	var args []any
	join := ""
	if ft := ftsQuery(q.Q); ft != "" {
		join += ` JOIN songs_fts f ON f.id = s.id AND f.songs_fts MATCH ?`
		args = append(args, ft)
	}
	if q.Artist != "" {
		join += ` JOIN song_artists sa ON sa.song_id = s.id AND sa.artist_id = ?`
		args = append(args, q.Artist)
	}
	if q.Album == SinglesAlbum {
		where = append(where, "s.album_id=''")
	} else if q.Album != "" {
		where = append(where, "s.album_id=?")
		args = append(args, q.Album)
	}
	if q.Genre != "" {
		where = append(where, "s.genre=?")
		args = append(args, q.Genre)
	}
	if q.Language != "" {
		where = append(where, "s.language=?")
		args = append(args, q.Language)
	}
	if q.OnlyMissing {
		where = append(where, "s.state IN('karaoke_ready','aligning','complete')")
	}
	if q.Unidentified {
		where = append(where, "(s.artist_display='' OR s.album_id='')")
	}
	if q.Cursor != "" {
		v, id, err := DecodeCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		op := ">"
		if dir == "DESC" {
			op = "<"
		}
		where = append(where, fmt.Sprintf("(%s,%s) %s (?,?)", col, idCol, op))
		if numeric {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, ErrBadCursor
			}
			args = append(args, n, id)
		} else {
			args = append(args, v, id)
		}
	} else if q.From != "" && col == "s.title_sort" {
		where = append(where, "s.title_sort >= ?")
		args = append(args, SortKey(q.From))
	}
	sqlText := `SELECT s.id,s.title,s.artist_display,s.album_title,s.duration_ms,s.state,s.has_lyrics,s.karaoke_version,` + coverIDSQL + `,` +
		col + ` FROM songs s` + join
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	sqlText += fmt.Sprintf(" ORDER BY %s %s, %s %s LIMIT ?", col, dir, idCol, dir)
	args = append(args, limit+1)

	rows, err := s.R.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[SongRow]{Items: []SongRow{}}
	var last any
	for rows.Next() {
		var r SongRow
		var sortVal any
		if err := rows.Scan(&r.ID, &r.Title, &r.Artist, &r.Album, &r.DurationMs, &r.State, &r.HasLyrics, &r.Karaoke, &r.CoverID, &sortVal); err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			c := EncodeCursor(fmt.Sprint(last), page.Items[limit-1].ID)
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, r)
		last = sortVal
	}
	return page, rows.Err()
}

// ArtistRow is a singer in a list.
type ArtistRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Gender    string `json:"gender,omitempty"`
	CoverID   string `json:"coverId,omitempty"`
	HasCustom bool   `json:"hasCustom,omitempty"`
	SongCount int    `json:"songCount,omitempty"`
}

const artistCoverSQL = `CASE WHEN a.cover_key<>'' THEN 'artist-'||a.id
	  ELSE COALESCE((SELECT al.id FROM albums al WHERE al.artist_id=a.id AND al.cover_key<>'' ORDER BY al.year DESC LIMIT 1),'') END`

// ListArtists returns singers A-Z.
func (s *Store) ListArtists(ctx context.Context, q, from, cursor string, limit int) (*Page[ArtistRow], error) {
	limit = ClampLimit(limit)
	var where []string
	var args []any
	if t := strings.TrimSpace(q); t != "" {
		where = append(where, "a.name LIKE ? ESCAPE '\\'")
		args = append(args, "%"+likeEscape(t)+"%")
	}
	if cursor != "" {
		v, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		where = append(where, "(a.name_sort,a.id) > (?,?)")
		args = append(args, v, id)
	} else if from != "" {
		where = append(where, "a.name_sort >= ?")
		args = append(args, SortKey(from))
	}
	text := `SELECT a.id,a.name,a.name_sort,a.gender,a.cover_key<>'',` + artistCoverSQL + `,
	  (SELECT COUNT(*) FROM song_artists sa WHERE sa.artist_id=a.id)
	  FROM artists a`
	if len(where) > 0 {
		text += " WHERE " + strings.Join(where, " AND ")
	}
	text += " ORDER BY a.name_sort, a.id LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.R.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[ArtistRow]{Items: []ArtistRow{}}
	lastSort := ""
	for rows.Next() {
		var r ArtistRow
		var sortVal string
		if err := rows.Scan(&r.ID, &r.Name, &sortVal, &r.Gender, &r.HasCustom, &r.CoverID, &r.SongCount); err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			c := EncodeCursor(lastSort, page.Items[limit-1].ID)
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, r)
		lastSort = sortVal
	}
	return page, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetArtist returns one singer's details.
func (s *Store) GetArtist(ctx context.Context, id string) (*ArtistRow, error) {
	var r ArtistRow
	err := s.R.QueryRowContext(ctx, `SELECT a.id,a.name,a.gender,a.cover_key<>'',`+artistCoverSQL+`,
	  (SELECT COUNT(*) FROM song_artists sa WHERE sa.artist_id=a.id)
	  FROM artists a WHERE a.id=?`, id).Scan(&r.ID, &r.Name, &r.Gender, &r.HasCustom, &r.CoverID, &r.SongCount)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateArtist renames a singer and updates all of their songs.
func (s *Store) UpdateArtist(ctx context.Context, id, name, gender string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE artists SET name=?, name_sort=?, gender=? WHERE id=?`,
		name, SortKey(name), gender, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	rows, err := tx.QueryContext(ctx, `SELECT song_id FROM song_artists WHERE artist_id=?`, id)
	if err != nil {
		return err
	}
	var songIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return err
		}
		songIDs = append(songIDs, sid)
	}
	rows.Close()
	for _, sid := range songIDs {
		arRows, err := tx.QueryContext(ctx, `SELECT a.name FROM song_artists sa JOIN artists a ON a.id=sa.artist_id WHERE sa.song_id=? ORDER BY sa.pos`, sid)
		if err != nil {
			return err
		}
		var names []string
		for arRows.Next() {
			var an string
			if err := arRows.Scan(&an); err != nil {
				arRows.Close()
				return err
			}
			names = append(names, an)
		}
		arRows.Close()
		if _, err := tx.ExecContext(ctx, `UPDATE songs SET artist_display=? WHERE id=?`, strings.Join(names, ", "), sid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM songs_fts WHERE id=?`, sid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO songs_fts(id,title,artists,album) SELECT id,title,artist_display,album_title FROM songs WHERE id=?`, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetArtistCover records or clears a singer's custom cover key.
func (s *Store) SetArtistCover(ctx context.Context, id, coverKey string) error {
	res, err := s.W.ExecContext(ctx, `UPDATE artists SET cover_key=? WHERE id=?`, coverKey, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SongArtist is one singer credited on a song.
type SongArtist struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Gender string `json:"gender,omitempty"`
}

// SongArtistsBySongIDs returns the ordered singers for each song ID.
func (s *Store) SongArtistsBySongIDs(ctx context.Context, ids []string) (map[string][]SongArtist, error) {
	out := make(map[string][]SongArtist, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.R.QueryContext(ctx, `SELECT sa.song_id,a.id,a.name,a.gender
	  FROM song_artists sa JOIN artists a ON a.id=sa.artist_id
	  WHERE sa.song_id IN(`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)
	  ORDER BY sa.song_id, sa.pos`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var a SongArtist
		if err := rows.Scan(&sid, &a.ID, &a.Name, &a.Gender); err != nil {
			return nil, err
		}
		out[sid] = append(out[sid], a)
	}
	return out, rows.Err()
}

// AlbumRow is an album in a list.
type AlbumRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ArtistID  string `json:"artistId"`
	Artist    string `json:"artist"`
	Year      int    `json:"year,omitempty"`
	HasCover  bool   `json:"hasCover"`
	SongCount int    `json:"songCount,omitempty"`
	CoverKey  string `json:"-"`
}

// ListAlbums returns albums A-Z, optionally for one singer.
func (s *Store) ListAlbums(ctx context.Context, q, artist, from, cursor string, limit int) (*Page[AlbumRow], error) {
	limit = ClampLimit(limit)
	var where []string
	var args []any
	if t := strings.TrimSpace(q); t != "" {
		where = append(where, "al.title LIKE ? ESCAPE '\\' OR COALESCE(a.name,'') LIKE ? ESCAPE '\\'")
		esc := "%" + likeEscape(t) + "%"
		args = append(args, esc, esc)
	}
	if artist != "" {
		where = append(where, "al.artist_id=?")
		args = append(args, artist)
	}
	if cursor != "" {
		v, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		where = append(where, "(al.title_sort,al.id) > (?,?)")
		args = append(args, v, id)
	} else if from != "" {
		where = append(where, "al.title_sort >= ?")
		args = append(args, SortKey(from))
	}
	text := `SELECT al.id,al.title,al.title_sort,al.artist_id,COALESCE(a.name,''),al.year,al.cover_key,
	  (SELECT COUNT(*) FROM songs s WHERE s.album_id=al.id)
	  FROM albums al LEFT JOIN artists a ON a.id=al.artist_id`
	if len(where) > 0 {
		text += " WHERE (" + strings.Join(where, ") AND (") + ")"
	}
	text += " ORDER BY al.title_sort, al.id LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.R.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[AlbumRow]{Items: []AlbumRow{}}
	lastSort := ""
	for rows.Next() {
		var r AlbumRow
		var sortVal string
		if err := rows.Scan(&r.ID, &r.Title, &sortVal, &r.ArtistID, &r.Artist, &r.Year, &r.CoverKey, &r.SongCount); err != nil {
			return nil, err
		}
		r.HasCover = r.CoverKey != ""
		if len(page.Items) == limit {
			c := EncodeCursor(lastSort, page.Items[limit-1].ID)
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, r)
		lastSort = sortVal
	}
	return page, rows.Err()
}

// GetAlbum returns one album's details.
func (s *Store) GetAlbum(ctx context.Context, id string) (*AlbumRow, error) {
	var r AlbumRow
	err := s.R.QueryRowContext(ctx, `SELECT al.id,al.title,al.artist_id,COALESCE(a.name,''),al.year,al.cover_key,
	  (SELECT COUNT(*) FROM songs s WHERE s.album_id=al.id)
	  FROM albums al LEFT JOIN artists a ON a.id=al.artist_id WHERE al.id=?`, id).
		Scan(&r.ID, &r.Title, &r.ArtistID, &r.Artist, &r.Year, &r.CoverKey, &r.SongCount)
	if err != nil {
		return nil, err
	}
	r.HasCover = r.CoverKey != ""
	return &r, nil
}

// UpdateAlbum changes an album's title and year, and updates its songs.
func (s *Store) UpdateAlbum(ctx context.Context, id, title string, year int) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE albums SET title=?, title_sort=?, year=? WHERE id=?`,
		title, SortKey(title), year, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `UPDATE songs SET album_title=? WHERE album_id=?`, title, id); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM songs WHERE album_id=?`, id)
	if err != nil {
		return err
	}
	var songIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return err
		}
		songIDs = append(songIDs, sid)
	}
	rows.Close()
	for _, sid := range songIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM songs_fts WHERE id=?`, sid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO songs_fts(id,title,artists,album) SELECT id,title,artist_display,album_title FROM songs WHERE id=?`, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetAlbumCover records or clears an album's cover and updates its songs.
func (s *Store) SetAlbumCover(ctx context.Context, id, coverKey, accentColor string) ([]string, error) {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE albums SET cover_key=? WHERE id=?`, coverKey, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	hasThumb := coverKey != ""
	if _, err := tx.ExecContext(ctx, `UPDATE songs SET has_thumb=?, accent_color=? WHERE album_id=?`, hasThumb, accentColor, id); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM songs WHERE album_id=?`, id)
	if err != nil {
		return nil, err
	}
	var songIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return nil, err
		}
		songIDs = append(songIDs, sid)
	}
	rows.Close()
	return songIDs, tx.Commit()
}

// ListDistinct pages through the distinct values of "genre" or "language".
func (s *Store) ListDistinct(ctx context.Context, column, cursor string, limit int) (*Page[string], error) {
	if column != "genre" && column != "language" {
		return nil, fmt.Errorf("bad column %q", column)
	}
	limit = ClampLimit(limit)
	after := ""
	if cursor != "" {
		v, _, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		after = v
	}
	rows, err := s.R.QueryContext(ctx, `SELECT DISTINCT `+column+` FROM songs WHERE `+column+`<>'' AND `+column+`>? ORDER BY `+column+` LIMIT ?`, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[string]{Items: []string{}}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			c := EncodeCursor(page.Items[limit-1], "")
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, v)
	}
	return page, rows.Err()
}

// AllMediaKeys streams every media key the library refers to.
func (s *Store) AllMediaKeys(ctx context.Context, fn func(key string) error) error {
	rows, err := s.R.QueryContext(ctx, `
	  SELECT audio_key FROM songs WHERE audio_key<>''
	  UNION ALL SELECT lyrics_key FROM songs WHERE lyrics_key<>''
	  UNION ALL SELECT video_key FROM songs WHERE video_key<>''
	  UNION ALL SELECT guide_key FROM songs WHERE guide_key<>''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return err
		}
		if err := fn(k); err != nil {
			return err
		}
	}
	return rows.Err()
}

// LyricKeys streams lyrics keys for backup.
func (s *Store) LyricKeys(ctx context.Context, fn func(key string) error) error {
	rows, err := s.R.QueryContext(ctx, `SELECT lyrics_key FROM songs WHERE lyrics_key<>''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return err
		}
		if err := fn(k); err != nil {
			return err
		}
	}
	return rows.Err()
}
