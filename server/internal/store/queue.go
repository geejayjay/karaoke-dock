package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// QueueItem joins a queue entry with its song's list fields.
type QueueItem struct {
	ID       int64   `json:"id"`
	Singer   string  `json:"singer"`
	ClientID string  `json:"-"`
	Position float64 `json:"-"`
	Status   string  `json:"status"`
	Song     SongRow `json:"song"`
}

const queueSelect = `SELECT q.id,q.singer,q.client_id,q.position,q.status,
  s.id,s.title,s.artist_display,s.album_title,s.duration_ms,s.state,s.has_lyrics,s.karaoke_version,` + coverIDSQL + `
  FROM queue_items q JOIN songs s ON s.id=q.song_id `

func scanQueue(r interface{ Scan(...any) error }) (*QueueItem, error) {
	var q QueueItem
	err := r.Scan(&q.ID, &q.Singer, &q.ClientID, &q.Position, &q.Status,
		&q.Song.ID, &q.Song.Title, &q.Song.Artist, &q.Song.Album, &q.Song.DurationMs, &q.Song.State,
		&q.Song.HasLyrics, &q.Song.Karaoke, &q.Song.CoverID)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// AddToQueue appends an item and returns its ID.
func (s *Store) AddToQueue(ctx context.Context, songID, singer, clientID string) (int64, error) {
	res, err := s.W.ExecContext(ctx, `INSERT INTO queue_items(song_id,singer,client_id,position,added_at)
	  VALUES(?,?,?,COALESCE((SELECT MAX(position) FROM queue_items),0)+1,?)`,
		songID, singer, clientID, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// NextForClient returns a guest's first waiting song and how many are ahead of
// it, or nil when they have none waiting.
func (s *Store) NextForClient(ctx context.Context, clientID string) (*QueueItem, int, error) {
	item, err := scanQueue(s.R.QueryRowContext(ctx, queueSelect+`WHERE q.status='waiting' AND q.client_id=? ORDER BY q.position,q.id LIMIT 1`, clientID))
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var ahead int
	err = s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items WHERE status='waiting' AND (position,id) < (?,?)`, item.Position, item.ID).Scan(&ahead)
	return item, ahead, err
}

// SungBefore pages through the songs a guest has queued, most recent first.
func (s *Store) SungBefore(ctx context.Context, clientID, cursor string, limit int) (*Page[SongRow], error) {
	limit = ClampLimit(limit)
	having := ""
	args := []any{clientID}
	if cursor != "" {
		v, _, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		last, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		having = ` HAVING MAX(q.id) < ?`
		args = append(args, last)
	}
	args = append(args, limit+1)
	rows, err := s.R.QueryContext(ctx, `SELECT s.id,s.title,s.artist_display,s.album_title,s.duration_ms,s.state,s.has_lyrics,s.karaoke_version,`+coverIDSQL+`,MAX(q.id)
	  FROM queue_items q JOIN songs s ON s.id=q.song_id
	  WHERE q.client_id=? AND s.state<>'failed' GROUP BY s.id`+having+` ORDER BY MAX(q.id) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[SongRow]{Items: []SongRow{}}
	var lastID int64
	for rows.Next() {
		var r SongRow
		var queueID int64
		if err := rows.Scan(&r.ID, &r.Title, &r.Artist, &r.Album, &r.DurationMs, &r.State, &r.HasLyrics, &r.Karaoke, &r.CoverID, &queueID); err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			c := EncodeCursor(strconv.FormatInt(lastID, 10), "")
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, r)
		lastID = queueID
	}
	return page, rows.Err()
}

// queuedLimit caps the IDs sent to phones, far above any real queue.
const queuedLimit = 500

// QueuedSongIDs lists the songs waiting or playing, so phones can mark them.
func (s *Store) QueuedSongIDs(ctx context.Context) ([]string, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT DISTINCT q.song_id FROM queue_items q JOIN songs s ON s.id=q.song_id
	  WHERE q.status IN('waiting','playing') AND s.state<>'failed' LIMIT ?`, queuedLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SongQueued reports whether a song is already waiting or playing. A song that
// failed doesn't count, so it can be tried again.
func (s *Store) SongQueued(ctx context.Context, songID string) (bool, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items q JOIN songs s ON s.id=q.song_id
	  WHERE q.song_id=? AND q.status IN('waiting','playing') AND s.state<>'failed'`, songID).Scan(&n)
	return n > 0, err
}

// CountSinger counts a singer's waiting items.
func (s *Store) CountSinger(ctx context.Context, clientID string) (int, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items WHERE status='waiting' AND client_id=?`, clientID).Scan(&n)
	return n, err
}

// GetQueueItem returns one item.
func (s *Store) GetQueueItem(ctx context.Context, id int64) (*QueueItem, error) {
	return scanQueue(s.R.QueryRowContext(ctx, queueSelect+`WHERE q.id=?`, id))
}

// CurrentItem returns the playing item, or nil.
func (s *Store) CurrentItem(ctx context.Context) (*QueueItem, error) {
	q, err := scanQueue(s.R.QueryRowContext(ctx, queueSelect+`WHERE q.status='playing' ORDER BY q.position LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return q, err
}

// ListQueue pages through waiting items in queue order.
func (s *Store) ListQueue(ctx context.Context, cursor string, limit int) (*Page[QueueItem], error) {
	limit = ClampLimit(limit)
	where := `WHERE q.status='waiting'`
	var args []any
	if cursor != "" {
		v, id, err := DecodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		p, err1 := strconv.ParseFloat(v, 64)
		n, err2 := strconv.ParseInt(id, 10, 64)
		if err1 != nil || err2 != nil {
			return nil, ErrBadCursor
		}
		where += ` AND (q.position,q.id) > (?,?)`
		args = append(args, p, n)
	}
	args = append(args, limit+1)
	rows, err := s.R.QueryContext(ctx, queueSelect+where+` ORDER BY q.position,q.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &Page[QueueItem]{Items: []QueueItem{}}
	for rows.Next() {
		q, err := scanQueue(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			l := page.Items[limit-1]
			c := EncodeCursor(strconv.FormatFloat(l.Position, 'g', -1, 64), strconv.FormatInt(l.ID, 10))
			page.NextCursor = &c
			break
		}
		page.Items = append(page.Items, *q)
	}
	return page, rows.Err()
}

// RemoveQueueItem deletes a waiting item.
func (s *Store) RemoveQueueItem(ctx context.Context, id int64) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM queue_items WHERE id=? AND status='waiting'`, id)
	return err
}

// SetQueueStatus changes an item's status.
func (s *Store) SetQueueStatus(ctx context.Context, id int64, status string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE queue_items SET status=? WHERE id=?`, status, id)
	return err
}

// MoveQueueItem places an item right after afterID, or first when afterID is 0.
func (s *Store) MoveQueueItem(ctx context.Context, id, afterID int64) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pos float64
	if afterID == 0 {
		var low sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(position) FROM queue_items WHERE status='waiting' AND id<>?`, id).Scan(&low); err != nil {
			return err
		}
		pos = low.Float64 - 1
	} else {
		var after float64
		if err := tx.QueryRowContext(ctx, `SELECT position FROM queue_items WHERE id=?`, afterID).Scan(&after); err != nil {
			return err
		}
		var next sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(position) FROM queue_items WHERE status='waiting' AND position>? AND id<>?`, after, id).Scan(&next); err != nil {
			return err
		}
		if next.Valid {
			pos = (after + next.Float64) / 2
		} else {
			pos = after + 1
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE queue_items SET position=? WHERE id=? AND status='waiting'`, pos, id); err != nil {
		return err
	}
	return tx.Commit()
}

// NextPlayable returns the first waiting item accepted by canPlay.
func (s *Store) NextPlayable(ctx context.Context, canPlay func(state string) bool) (*QueueItem, error) {
	cursor := ""
	for {
		page, err := s.ListQueue(ctx, cursor, MaxLimit)
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			if canPlay(page.Items[i].Song.State) {
				return &page.Items[i], nil
			}
		}
		if page.NextCursor == nil {
			return nil, nil
		}
		cursor = *page.NextCursor
	}
}

// IsSongActive reports whether a song is playing right now.
func (s *Store) IsSongActive(ctx context.Context, songID string) (bool, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_items WHERE status='playing' AND song_id=?`, songID).Scan(&n)
	return n > 0, err
}

// EnqueueJob adds or resets one pipeline stage.
func (s *Store) EnqueueJob(ctx context.Context, songID, stage string) error {
	_, err := s.W.ExecContext(ctx, `INSERT INTO jobs(song_id,stage,status,attempts,error,updated_at) VALUES(?,?,'pending',0,'',?)
	  ON CONFLICT(song_id,stage) DO UPDATE SET status='pending',attempts=0,error='',run_after=0,blocked=0,updated_at=excluded.updated_at`,
		songID, stage, time.Now().UnixMilli())
	return err
}

// EnqueueJobOnce adds a stage unless it already exists.
func (s *Store) EnqueueJobOnce(ctx context.Context, songID, stage string) error {
	_, err := s.W.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(song_id,stage,status,attempts,error,updated_at) VALUES(?,?,'pending',0,'',?)`,
		songID, stage, time.Now().UnixMilli())
	return err
}

// JobStatus returns a stage's status, or "" when it doesn't exist.
func (s *Store) JobStatus(ctx context.Context, songID, stage string) (string, error) {
	var status string
	err := s.R.QueryRowContext(ctx, `SELECT status FROM jobs WHERE song_id=? AND stage=?`, songID, stage).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return status, err
}

// Job is one claimed pipeline stage.
type Job struct {
	SongID, Stage string
	Attempts      int
	Blocked       int // times this stage was blocked and put off
}

// ClaimJob atomically takes the next job from the given stages. It returns nil
// when none is ready. AI stages use the queue-aware order from section 5.
func (s *Store) ClaimJob(ctx context.Context, stages []string) (*Job, error) {
	if len(stages) == 0 {
		return nil, nil
	}
	ph := ""
	args := make([]any, 0, len(stages))
	for i, st := range stages {
		if i > 0 {
			ph += ","
		}
		ph += "?"
		args = append(args, st)
	}
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var j Job
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`
	  SELECT j.song_id,j.stage,j.attempts,j.blocked FROM jobs j
	  LEFT JOIN (SELECT song_id,MIN(CASE status WHEN 'playing' THEN -1e18 ELSE position END) p
	             FROM queue_items WHERE status IN('waiting','playing') GROUP BY song_id) q ON q.song_id=j.song_id
	  WHERE j.status='pending' AND j.run_after<=? AND j.stage IN(%s)
	  ORDER BY CASE WHEN q.p IS NULL THEN 2 WHEN j.stage='align' THEN 1 ELSE 0 END, q.p, j.updated_at LIMIT 1`, ph),
		append([]any{time.Now().UnixMilli()}, args...)...).
		Scan(&j.SongID, &j.Stage, &j.Attempts, &j.Blocked)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,updated_at=? WHERE song_id=? AND stage=?`,
		time.Now().UnixMilli(), j.SongID, j.Stage); err != nil {
		return nil, err
	}
	j.Attempts++
	return &j, tx.Commit()
}

// FinishJob records a stage result.
func (s *Store) FinishJob(ctx context.Context, songID, stage, status, errText string) error {
	_, err := s.W.ExecContext(ctx, `UPDATE jobs SET status=?,error=?,updated_at=? WHERE song_id=? AND stage=?`,
		status, errText, time.Now().UnixMilli(), songID, stage)
	return err
}

// RetryJobLater puts a stage off until a set time. It doesn't use up the
// stage's normal retries.
func (s *Store) RetryJobLater(ctx context.Context, songID, stage, errText string, at time.Time) error {
	_, err := s.W.ExecContext(ctx, `UPDATE jobs SET status='pending',attempts=0,blocked=blocked+1,run_after=?,error=?,updated_at=? WHERE song_id=? AND stage=?`,
		at.UnixMilli(), errText, time.Now().UnixMilli(), songID, stage)
	return err
}

// ResetRunningJobs returns jobs interrupted by a shutdown to pending.
func (s *Store) ResetRunningJobs(ctx context.Context) error {
	_, err := s.W.ExecContext(ctx, `UPDATE jobs SET status='pending' WHERE status='running'`)
	return err
}

// ClearJobs removes all stages of a song, for reprocessing.
func (s *Store) ClearJobs(ctx context.Context, songID string) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM jobs WHERE song_id=?`, songID)
	return err
}

// Session storage for the admin panel.

// SaveSession stores a hashed token.
func (s *Store) SaveSession(ctx context.Context, hash string, expires time.Time) error {
	_, err := s.W.ExecContext(ctx, `INSERT INTO admin_sessions(token_hash,expires_at) VALUES(?,?)`, hash, expires.Unix())
	return err
}

// SessionValid checks a hashed token.
func (s *Store) SessionValid(ctx context.Context, hash string) (bool, error) {
	var exp int64
	err := s.R.QueryRowContext(ctx, `SELECT expires_at FROM admin_sessions WHERE token_hash=?`, hash).Scan(&exp)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exp > time.Now().Unix(), nil
}

// DeleteSession removes one token.
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token_hash=?`, hash)
	return err
}

// DeleteAllSessions signs everyone out.
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM admin_sessions`)
	return err
}

// PruneSessions removes expired tokens.
func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at<?`, time.Now().Unix())
	return err
}
