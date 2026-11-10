package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Song groups for the admin status list. A song is processed once its audio and
// its lyrics are both finished.
const (
	FilterAll     = "all"
	FilterWorking = "working"
	FilterDone    = "done"
	FilterProblem = "problem"

	processedSQL = `(state='complete' AND audio_key<>'' AND has_lyrics=1)`
	problemSQL   = `(state='failed' OR (state='playable' AND error<>'') OR (state='complete' AND (audio_key='' OR has_lyrics=0)))`
)

// ClientTag is a public stand-in for a guest's private client ID. It lets a
// phone recognise its own song without the ID being sent to everyone.
func ClientTag(clientID string) string {
	if clientID == "" {
		return ""
	}
	h := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(h[:8])
}

// CoverID is the album whose art this song shows, or "" when there is none.
func (s *Song) CoverID() string {
	if s.HasThumb {
		return s.AlbumID
	}
	return ""
}

// Row returns the small list shape of a song.
func (s *Song) Row() SongRow {
	return SongRow{ID: s.ID, Title: s.Title, Artist: s.Artist, Album: s.Album, DurationMs: s.DurationMs,
		State: s.State, HasLyrics: s.HasLyrics, Karaoke: s.KaraokeVersion, CoverID: s.CoverID()}
}

// SongRowsByID returns the library rows for the given IDs. Missing IDs are left out.
func (s *Store) SongRowsByID(ctx context.Context, ids []string) (map[string]SongRow, error) {
	out := make(map[string]SongRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.R.QueryContext(ctx, `SELECT s.id,s.title,s.artist_display,s.album_title,s.duration_ms,s.state,s.has_lyrics,s.karaoke_version,`+coverIDSQL+`
	  FROM songs s WHERE s.id IN(`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SongRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Artist, &r.Album, &r.DurationMs, &r.State, &r.HasLyrics, &r.Karaoke, &r.CoverID); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

func processingWhere(filter string) (string, error) {
	switch filter {
	case "", FilterAll:
		return "1=1", nil
	case FilterDone:
		return processedSQL, nil
	case FilterProblem:
		return problemSQL, nil
	case FilterWorking:
		return "NOT " + processedSQL + " AND NOT " + problemSQL, nil
	}
	return "", fmt.Errorf("%w: %q", ErrBadFilter, filter)
}

// ListProcessing pages through songs, newest first, for the status list.
func (s *Store) ListProcessing(ctx context.Context, filter, cursor string, limit int) (*Page[Song], error) {
	limit = ClampLimit(limit)
	where, err := processingWhere(filter)
	if err != nil {
		return nil, err
	}
	var args []any
	if cursor != "" {
		v, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		where += ` AND (created_at,id) < (?,?)`
		args = append(args, n, id)
	}
	args = append(args, limit+1)
	rows, err := s.R.QueryContext(ctx, `SELECT `+songCols+` FROM songs WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[Song]{Items: []Song{}}
	for rows.Next() {
		song, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			l := page.Items[limit-1]
			c := EncodeCursor(strconv.FormatInt(l.CreatedAt, 10), l.ID)
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, *song)
	}
	return page, rows.Err()
}

// ProcessingCounts is the size of each group in the status list.
type ProcessingCounts struct {
	All     int `json:"all"`
	Working int `json:"working"`
	Done    int `json:"done"`
	Problem int `json:"problem"`
}

// ArtistExists reports whether a singer is already in the library.
func (s *Store) ArtistExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM artists WHERE name_sort=?`, SortKey(name)).Scan(&n)
	return n > 0, err
}

// SongIDsWithAudio lists every song that has finished audio.
func (s *Store) SongIDsWithAudio(ctx context.Context) ([]string, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id FROM songs WHERE audio_key<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountProcessing counts songs in each group.
func (s *Store) CountProcessing(ctx context.Context) (ProcessingCounts, error) {
	var c ProcessingCounts
	err := s.R.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(CASE WHEN `+processedSQL+` THEN 1 END), COUNT(CASE WHEN `+problemSQL+` THEN 1 END) FROM songs`).
		Scan(&c.All, &c.Done, &c.Problem)
	c.Working = c.All - c.Done - c.Problem
	return c, err
}
