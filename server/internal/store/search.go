package store

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

const (
	maxTrigrams  = 32
	fuzzyPool    = 200
	minNameShare = 0.55 // how many of the typed words must appear in a song's name
	minTypoWord  = 4    // shorter words have to be spelled right
	longTypoWord = 8    // words this long may have two slips
)

// trigrams splits text into overlapping three-letter pieces, one set per word.
func trigrams(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		runes := []rune(word)
		for i := 0; i+3 <= len(runes) && len(out) < maxTrigrams; i++ {
			if t := string(runes[i : i+3]); !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func compact(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// fuzzyScore rates a song against a search, or returns 0 for no match. A phrase
// found in the lyrics counts fully, and a name can match despite typos.
func fuzzyScore(query, name, lyrics string) float64 {
	best := 0.0
	phrase := compact(query)
	if len(phrase) >= 4 && strings.Contains(compact(lyrics), phrase) {
		best = 0.9
	}
	typed := strings.Fields(phrase)
	nameWords := strings.Fields(compact(name))
	found := 0
	for _, word := range typed {
		for _, candidate := range nameWords {
			if similarWord(word, candidate) {
				found++
				break
			}
		}
	}
	if len(typed) > 0 {
		if share := float64(found) / float64(len(typed)); share >= minNameShare && share > best {
			best = share
		}
	}
	return best
}

// similarWord allows a slip or two, but short words have to match exactly.
func similarWord(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < minTypoWord || len(rb) < minTypoWord {
		return false
	}
	allowed := 1
	if len(ra) >= longTypoWord {
		allowed = 2
	}
	return typoDistance(ra, rb) <= allowed
}

// typoDistance counts the wrong, missing, extra or swapped letters between two words.
func typoDistance(a, b []rune) int {
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}
	for j := range rows[0] {
		rows[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}
	return rows[len(a)][len(b)]
}

// FuzzySongs finds songs when an exact search found none, allowing for typos in
// the title or singer, and matching phrases from the lyrics.
func (s *Store) FuzzySongs(ctx context.Context, query string, limit int) (*Page[SongRow], error) {
	limit = ClampLimit(limit)
	page := &Page[SongRow]{Items: []SongRow{}}
	grams := trigrams(query)
	if len(grams) == 0 {
		return page, nil
	}
	quoted := make([]string, len(grams))
	for i, g := range grams {
		quoted[i] = `"` + g + `"`
	}
	rows, err := s.R.QueryContext(ctx, `SELECT s.id,s.title,s.artist_display,s.album_title,s.duration_ms,s.state,s.has_lyrics,s.karaoke_version,`+coverIDSQL+`,songs_tri.name,songs_tri.lyrics
	  FROM songs_tri JOIN songs s ON s.id=songs_tri.id
	  WHERE songs_tri MATCH ? AND s.state<>'failed' ORDER BY rank LIMIT ?`, strings.Join(quoted, " OR "), fuzzyPool)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		row   SongRow
		score float64
	}
	var hits []scored
	for rows.Next() {
		var r SongRow
		var name, lyrics string
		if err := rows.Scan(&r.ID, &r.Title, &r.Artist, &r.Album, &r.DurationMs, &r.State, &r.HasLyrics, &r.Karaoke, &r.CoverID, &name, &lyrics); err != nil {
			return nil, err
		}
		if score := fuzzyScore(query, name, lyrics); score > 0 {
			hits = append(hits, scored{r, score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	for i := 0; i < len(hits) && i < limit; i++ {
		page.Items = append(page.Items, hits[i].row)
	}
	return page, nil
}

// SetLyricsText stores a song's words so they can be searched.
func (s *Store) SetLyricsText(ctx context.Context, songID, text string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE songs_tri SET lyrics=? WHERE id=?`, text, songID)
	return err
}

// SongsNeedingLyricsText lists songs with lyrics that aren't searchable yet.
func (s *Store) SongsNeedingLyricsText(ctx context.Context) ([]string, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT s.id FROM songs s JOIN songs_tri t ON t.id=s.id WHERE s.lyrics_key<>'' AND t.lyrics=''`)
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

// SetSinger changes the name shown on a queued song.
func (s *Store) SetSinger(ctx context.Context, itemID int64, singer string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE queue_items SET singer=? WHERE id=?`, singer, itemID)
	return err
}

// ClearQueue removes every waiting song, and leaves the one playing.
func (s *Store) ClearQueue(ctx context.Context) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM queue_items WHERE status='waiting'`)
	return err
}
