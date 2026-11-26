package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"karaokedock/internal/media"
	"karaokedock/internal/settings"
)

// MoveStatus reports a storage move.
type MoveStatus struct {
	Running bool   `json:"running"`
	Done    int    `json:"done"`
	Target  string `json:"target"`
	Error   string `json:"error,omitempty"`
}

// CurrentMove returns the latest status, or nil.
func (m *Manager) CurrentMove() *MoveStatus { return m.move.Load() }

// StartMove copies all media to a new folder in the background.
func (m *Manager) StartMove(ctx context.Context, target string) error {
	if s := m.move.Load(); s != nil && s.Running {
		return ErrMoveRunning
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("can't use that folder: %w", err)
	}
	m.move.Store(&MoveStatus{Running: true, Target: abs})
	go m.runMove(ctx, abs)
	return nil
}

func (m *Manager) runMove(ctx context.Context, target string) {
	// Waits for running jobs to finish, and holds new ones until the move ends.
	m.storage.Lock()
	defer m.storage.Unlock()
	old := m.Media.Root()
	done := 0
	var keys []string
	err := m.DB.AllMediaKeys(ctx, func(key string) error {
		src, err := media.JoinKey(old, key)
		if err != nil {
			return err
		}
		if _, statErr := os.Stat(src); os.IsNotExist(statErr) {
			return nil // already missing, shown on the Library page
		}
		dst, err := media.JoinKey(target, key)
		if err != nil {
			return err
		}
		if err := copyVerified(src, dst); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		keys = append(keys, key)
		done++
		m.move.Store(&MoveStatus{Running: true, Done: done, Target: target})
		return nil
	})
	if err == nil {
		err = m.Settings.SetRaw(ctx, map[string]string{settings.MediaRoot: target})
	}
	if err != nil {
		slog.Error("media move failed", "err", err)
		m.move.Store(&MoveStatus{Done: done, Target: target, Error: "Move failed. Your files were left where they were."})
		return
	}
	m.Media.SetRoot(target)
	for _, key := range keys {
		if src, err := media.JoinKey(old, key); err == nil {
			_ = os.Remove(src)
		}
	}
	m.move.Store(&MoveStatus{Done: done, Target: target})
}

func copyVerified(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	n, err := media.AtomicCopy(dst, in)
	if err != nil {
		return err
	}
	if n != st.Size() {
		return fmt.Errorf("size mismatch")
	}
	a, err := media.HashFile(src)
	if err != nil {
		return err
	}
	b, err := media.HashFile(dst)
	if err != nil {
		return err
	}
	if a != b {
		_ = os.Remove(dst)
		return fmt.Errorf("hash mismatch")
	}
	return nil
}
