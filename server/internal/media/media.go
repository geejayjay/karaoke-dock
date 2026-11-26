// Package media is the single path for reading and writing media files.
package media

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// File is a readable, seekable media file.
type File interface {
	io.ReadSeekCloser
}

// Info describes a stored object.
type Info struct {
	Size    int64
	ModTime time.Time
}

// Store is the storage interface used by the whole server. S3 could implement
// it later without touching the pipeline.
type Store interface {
	Open(key string) (File, error)
	Write(key string, r io.Reader) (int64, error)
	Delete(key string) error
	Stat(key string) (Info, error)
}

// ErrUnavailable means the media root cannot be reached.
var ErrUnavailable = errors.New("media root unavailable")

// Fs stores objects below a root folder.
type Fs struct {
	root    atomic.Value // string
	healthy atomic.Bool
}

// NewFs creates a filesystem store and creates the root if it can.
func NewFs(root string) *Fs {
	f := &Fs{}
	f.root.Store(root)
	_ = os.MkdirAll(root, 0o755)
	f.Check()
	return f
}

// Root returns the current folder.
func (f *Fs) Root() string { return f.root.Load().(string) }

// SetRoot switches to another folder. The caller has already moved the files.
func (f *Fs) SetRoot(root string) {
	f.root.Store(root)
	f.Check()
}

// Healthy reports the result of the last check.
func (f *Fs) Healthy() bool { return f.healthy.Load() }

// Check tests the root by writing and removing a small file.
func (f *Fs) Check() bool {
	probe := filepath.Join(f.Root(), ".health-"+fmt.Sprint(time.Now().UnixNano()))
	ok := os.WriteFile(probe, []byte("ok"), 0o644) == nil
	if ok {
		_ = os.Remove(probe)
	}
	f.healthy.Store(ok)
	return ok
}

// Watch re-checks the root every 30 seconds until ctx ends. onChange is called
// whenever health flips.
func (f *Fs) Watch(ctx context.Context, onChange func(healthy bool)) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	prev := f.Healthy()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if now := f.Check(); now != prev {
				prev = now
				onChange(now)
			}
		}
	}
}

// Path converts a key to a real path, refusing anything that leaves the root.
func (f *Fs) Path(key string) (string, error) {
	return JoinKey(f.Root(), key)
}

// JoinKey resolves a key below root safely.
func JoinKey(root, key string) (string, error) {
	if key == "" || strings.Contains(key, "..") || strings.ContainsAny(key, `\:`) || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("bad media key %q", key)
	}
	return filepath.Join(root, filepath.FromSlash(key)), nil
}

// Open opens an object.
func (f *Fs) Open(key string) (File, error) {
	if !f.Healthy() {
		return nil, ErrUnavailable
	}
	p, err := f.Path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Write stores an object atomically: temp file on the same drive, then rename.
func (f *Fs) Write(key string, r io.Reader) (int64, error) {
	if !f.Healthy() {
		return 0, ErrUnavailable
	}
	p, err := f.Path(key)
	if err != nil {
		return 0, err
	}
	return AtomicCopy(p, r)
}

// AtomicCopy writes r to path via a temp file in the same folder.
func AtomicCopy(path string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return n, nil
}

// Delete removes an object. A missing object is not an error.
func (f *Fs) Delete(key string) error {
	p, err := f.Path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Stat returns an object's size and time.
func (f *Fs) Stat(key string) (Info, error) {
	p, err := f.Path(key)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Info{}, err
	}
	return Info{Size: st.Size(), ModTime: st.ModTime()}, nil
}

// AudioKey is the key of a finished song's audio.
func AudioKey(songID, ext string) string { return shard("audio", songID, ext) }

// LyricsKey is the key of a song's lyrics.
func LyricsKey(songID string) string { return shard("lyrics", songID, ".json") }

// VideoKey is the key of a kept video.
func VideoKey(songID string) string { return shard("video", songID, ".mp4") }

// GuideKey is the key of a kept guide vocal.
func GuideKey(songID, ext string) string { return shard("guide", songID, ext) }

func shard(kind, id, ext string) string {
	p := "00"
	if len(id) >= 2 {
		// The last characters are random for every kind of ID. Folder names
		// can collide on case-insensitive disks, which is fine, because the
		// file name keeps the exact ID.
		p = strings.ToLower(id[len(id)-2:])
	}
	return kind + "/" + p + "/" + id + ext
}

// HashFile returns the SHA-256 of a local file.
func HashFile(path string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return out, err
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}
