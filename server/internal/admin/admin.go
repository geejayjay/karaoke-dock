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
	"math/big"
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
	mustChangeKey   = "ADMIN_MUST_CHANGE"
	passwordLen     = 24
	sessionLifetime = 12 * time.Hour
	initialFile     = "initial-admin-password.txt"
)

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

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

	mu       sync.Mutex
	failures int
	lockedTo time.Time
}

// New creates the service.
func New(db *store.Store, s *settings.Manager, metadataDir string) *Service {
	return &Service{db: db, settings: s, metadataDir: metadataDir}
}

func generatePassword() string {
	b := make([]byte, passwordLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
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

func (s *Service) initialPath() string { return filepath.Join(s.metadataDir, initialFile) }

// EnsurePassword generates the password on first start and shows it once.
func (s *Service) EnsurePassword(ctx context.Context) error {
	if s.settings.Get(hashKey) != "" {
		return nil
	}
	pw, err := s.setGenerated(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("\n==== KaraokeDock admin password (shown once) ====\n%s\nAlso saved to: %s\n=================================================\n\n", pw, s.initialPath())
	return nil
}

// ResetPassword makes a new password. It runs from the command line.
func (s *Service) ResetPassword(ctx context.Context) (string, error) {
	pw, err := s.setGenerated(ctx)
	if err != nil {
		return "", err
	}
	return pw, s.db.DeleteAllSessions(ctx)
}

func (s *Service) setGenerated(ctx context.Context) (string, error) {
	pw := generatePassword()
	h, err := hashPassword(pw)
	if err != nil {
		return "", err
	}
	if err := s.settings.SetRaw(ctx, map[string]string{hashKey: h, mustChangeKey: "true"}); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.initialPath(), []byte(pw+"\n"), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

// MustChange reports whether the first-login change is outstanding.
func (s *Service) MustChange() bool { return s.settings.Bool(mustChangeKey) }

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

// ChangePassword replaces the password and removes the initial file.
func (s *Service) ChangePassword(ctx context.Context, current, next string) error {
	if !s.Allow() {
		return ErrThrottled
	}
	if !verifyPassword(s.settings.Get(hashKey), current) {
		s.Failed()
		return errors.New("current password is wrong")
	}
	if len(next) < 8 {
		return errors.New("use at least 8 characters")
	}
	s.Succeeded()
	h, err := hashPassword(next)
	if err != nil {
		return err
	}
	if err := s.settings.SetRaw(ctx, map[string]string{hashKey: h, mustChangeKey: "false"}); err != nil {
		return err
	}
	if err := os.Remove(s.initialPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
