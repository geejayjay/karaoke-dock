// Package admin handles the admin password and sessions.
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"karaokedock/internal/settings"
	"karaokedock/internal/store"
)

const (
	hashKey         = "ADMIN_PASSWORD_HASH"
	mustChangeKey   = "ADMIN_MUST_CHANGE" // only set by older versions
	minPasswordLen  = 8
	sessionLifetime = 12 * time.Hour
)

// Argon2id cost.
const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// Service manages the password and sessions.
type Service struct {
	db          *store.Store
	settings    *settings.Manager
	metadataDir string

	setupMu  sync.Mutex
	mu       sync.Mutex
	failures int
	lockedTo time.Time
}

// New creates the service.
func New(db *store.Store, s *settings.Manager, metadataDir string) *Service {
	return &Service{db: db, settings: s, metadataDir: metadataDir}
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", argonTime, argonMemory, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, pw string) bool {
	var t, m uint32
	var p uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	if _, err := fmt.Sscan(parts[1]+" "+parts[2]+" "+parts[3], &t, &m, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// legacyFile held the generated password of older versions.
const legacyFile = "initial-admin-password.txt"

// Prepare runs at start. Older versions made up a first password and asked
// for a new one at the first sign-in. If that never happened, the password is
// cleared so the admin page asks for one instead.
func (s *Service) Prepare(ctx context.Context) error {
	if s.settings.Bool(mustChangeKey) {
		if err := s.settings.SetRaw(ctx, map[string]string{hashKey: "", mustChangeKey: "false"}); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(s.metadataDir, legacyFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.NeedsSetup() {
		fmt.Print("\nNo admin password yet. Open the admin page to create one.\n\n")
	}
	return nil
}

// NeedsSetup reports whether no admin password has been created yet.
func (s *Service) NeedsSetup() bool { return s.settings.Get(hashKey) == "" }

// ErrSetupDone means a password already exists.
var ErrSetupDone = errors.New("a password has already been created")

// Setup creates the first admin password. It only works while there is none.
func (s *Service) Setup(ctx context.Context, pw string) error {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if !s.NeedsSetup() {
		return ErrSetupDone
	}
	if len(pw) < minPasswordLen {
		return errors.New("use at least 8 characters")
	}
	h, err := hashPassword(pw)
	if err != nil {
		return err
	}
	return s.settings.SetRaw(ctx, map[string]string{hashKey: h})
}

// ResetPassword removes the password and signs everyone out, so the admin page
// asks for a new one. It runs from the command line.
func (s *Service) ResetPassword(ctx context.Context) error {
	if err := s.settings.SetRaw(ctx, map[string]string{hashKey: "", mustChangeKey: "false"}); err != nil {
		return err
	}
	return s.db.DeleteAllSessions(ctx)
}

// ErrThrottled means too many failed logins.
var ErrThrottled = errors.New("too many attempts")

// Allow checks the shared login throttle: free for 5 failures, then the wait
// doubles up to 5 minutes.
func (s *Service) Allow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().After(s.lockedTo)
}

// Failed records a failed login or PIN.
func (s *Service) Failed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	if s.failures >= 5 {
		d := time.Second << min(s.failures-5, 8)
		s.lockedTo = time.Now().Add(min(d, 5*time.Minute))
	}
}

// Succeeded clears the throttle.
func (s *Service) Succeeded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = 0
	s.lockedTo = time.Time{}
}

// Login checks the password and returns a new session token.
func (s *Service) Login(ctx context.Context, pw string) (string, error) {
	if !s.Allow() {
		return "", ErrThrottled
	}
	if !verifyPassword(s.settings.Get(hashKey), pw) {
		s.Failed()
		return "", errors.New("wrong password")
	}
	s.Succeeded()
	_ = s.db.PruneSessions(ctx)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, s.db.SaveSession(ctx, hashToken(token), time.Now().Add(sessionLifetime))
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// SessionLifetime is how long a login lasts.
func SessionLifetime() time.Duration { return sessionLifetime }

// Valid checks a session token.
func (s *Service) Valid(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	ok, err := s.db.SessionValid(ctx, hashToken(token))
	return err == nil && ok
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.db.DeleteSession(ctx, hashToken(token))
}

// ChangePassword replaces the password.
func (s *Service) ChangePassword(ctx context.Context, current, next string) error {
	if !s.Allow() {
		return ErrThrottled
	}
	if !verifyPassword(s.settings.Get(hashKey), current) {
		s.Failed()
		return errors.New("current password is wrong")
	}
	if len(next) < minPasswordLen {
		return errors.New("use at least 8 characters")
	}
	s.Succeeded()
	h, err := hashPassword(next)
	if err != nil {
		return err
	}
	return s.settings.SetRaw(ctx, map[string]string{hashKey: h})
}
