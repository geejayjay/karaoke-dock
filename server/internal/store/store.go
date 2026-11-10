// Package store owns all SQLite access. It keeps one writer connection and a
// pool of readers.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite" // SQLite driver
)

// Store wraps the reader pool and the single writer.
type Store struct {
	R *sql.DB
	W *sql.DB
}

const schema = `
CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE artists(
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  name_sort TEXT NOT NULL,
  gender TEXT NOT NULL DEFAULT ''
);
CREATE INDEX artists_sort ON artists(name_sort, id);

CREATE TABLE albums(
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  title_sort TEXT NOT NULL,
  artist_id TEXT NOT NULL DEFAULT '',
  year INTEGER NOT NULL DEFAULT 0,
  cover_key TEXT NOT NULL DEFAULT ''
);
CREATE INDEX albums_sort ON albums(title_sort, id);
CREATE INDEX albums_artist ON albums(artist_id, title_sort, id);

CREATE TABLE songs(
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  title_sort TEXT NOT NULL,
  artist_display TEXT NOT NULL DEFAULT '',
  album_id TEXT NOT NULL DEFAULT '',
  album_title TEXT NOT NULL DEFAULT '',
  genre TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'queued',
  error TEXT NOT NULL DEFAULT '',
  has_lyrics INTEGER NOT NULL DEFAULT 0,
  karaoke_version INTEGER NOT NULL DEFAULT 0,
  audio_key TEXT NOT NULL DEFAULT '',
  lyrics_key TEXT NOT NULL DEFAULT '',
  video_key TEXT NOT NULL DEFAULT '',
  guide_key TEXT NOT NULL DEFAULT '',
  accent_color TEXT NOT NULL DEFAULT '',
  has_thumb INTEGER NOT NULL DEFAULT 0,
  has_bg INTEGER NOT NULL DEFAULT 0,
  play_count INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX songs_title ON songs(title_sort, id);
CREATE INDEX songs_new ON songs(created_at DESC, id DESC);
CREATE INDEX songs_popular ON songs(play_count DESC, id DESC);
CREATE INDEX songs_album ON songs(album_id, title_sort, id);
CREATE INDEX songs_genre ON songs(genre, title_sort, id);
CREATE INDEX songs_language ON songs(language, title_sort, id);

CREATE TABLE song_artists(
  song_id TEXT NOT NULL,
  artist_id TEXT NOT NULL,
  pos INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(song_id, artist_id)
) WITHOUT ROWID;
CREATE INDEX song_artists_artist ON song_artists(artist_id, song_id);

CREATE VIRTUAL TABLE songs_fts USING fts5(
  id UNINDEXED, title, artists, album,
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE queue_items(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  song_id TEXT NOT NULL,
  singer TEXT NOT NULL,
  client_id TEXT NOT NULL DEFAULT '',
  position REAL NOT NULL,
  status TEXT NOT NULL DEFAULT 'waiting',
  added_at INTEGER NOT NULL
);
CREATE INDEX queue_active ON queue_items(status, position, id);
CREATE INDEX queue_song ON queue_items(song_id, status);

CREATE TABLE jobs(
  song_id TEXT NOT NULL,
  stage TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(song_id, stage)
) WITHOUT ROWID;
CREATE INDEX jobs_status ON jobs(status, updated_at);

CREATE TABLE admin_sessions(
  token_hash TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL
) WITHOUT ROWID;
`

const schemaVersion = 6

// searchSchema adds typo-friendly search over names and lyrics.
const searchSchema = `
CREATE VIRTUAL TABLE songs_tri USING fts5(id UNINDEXED, name, lyrics, tokenize='trigram');
INSERT INTO songs_tri(id,name,lyrics) SELECT id,title||' '||artist_display||' '||album_title,'' FROM songs;
CREATE TRIGGER songs_tri_add AFTER INSERT ON songs BEGIN
  INSERT INTO songs_tri(id,name,lyrics) VALUES(new.id,new.title||' '||new.artist_display||' '||new.album_title,'');
END;
CREATE TRIGGER songs_tri_del AFTER DELETE ON songs BEGIN
  DELETE FROM songs_tri WHERE id=old.id;
END;
CREATE TRIGGER songs_tri_name AFTER UPDATE OF title,artist_display,album_title ON songs BEGIN
  UPDATE songs_tri SET name=new.title||' '||new.artist_display||' '||new.album_title WHERE id=new.id;
END;
`

// Open opens or creates the database and applies the schema.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := func(rw string) string {
		q := url.Values{}
		q.Add("_pragma", "busy_timeout(10000)")
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Add("_pragma", "foreign_keys(ON)")
		q.Add("_pragma", "mmap_size(268435456)")
		q.Add("_pragma", "cache_size(-16000)")
		q.Set("mode", rw)
		return "file:" + filepath.ToSlash(abs) + "?" + q.Encode()
	}
	w, err := sql.Open("sqlite", dsn("rwc"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := migrate(w); err != nil {
		w.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	r, err := sql.Open("sqlite", dsn("ro"))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(max(4, runtime.NumCPU()))
	return &Store{R: r, W: w}, nil
}

// Close closes both pools.
func (s *Store) Close() error {
	_ = s.R.Close()
	return s.W.Close()
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v < 1 {
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
	}
	if v < 2 {
		if _, err := tx.Exec(`ALTER TABLE songs ADD COLUMN trim_start_ms INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE songs ADD COLUMN trim_end_ms INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if v < 3 { // background videos were removed
		if _, err := tx.Exec(`DROP TABLE IF EXISTS backgrounds; DELETE FROM settings WHERE key='DEFAULT_BACKGROUND'`); err != nil {
			return err
		}
	}
	if v < 4 {
		if _, err := tx.Exec(searchSchema); err != nil {
			return err
		}
	}
	if v < 5 {
		if _, err := tx.Exec(`ALTER TABLE songs ADD COLUMN timed_lines INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE songs ADD COLUMN lyric_lines INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if v < 6 {
		if _, err := tx.Exec(`ALTER TABLE artists ADD COLUMN cover_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// GetSetting returns one setting, or "" when unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// AllSettings returns every stored setting.
func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSettings saves several settings in one transaction.
func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
