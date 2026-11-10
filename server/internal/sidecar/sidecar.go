// Package sidecar starts, supervises and talks to the Python helper.
package sidecar

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"karaokedock/internal/tools"
)

// ProtocolVersion must match the helper's.
const ProtocolVersion = 1

// IdleShutdown is how long the helper stays running with no work.
const IdleShutdown = 5 * time.Minute

// Error is a failure reported by the helper.
type Error struct {
	Code, Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// IsGPUOOM reports a GPU out-of-memory failure.
func IsGPUOOM(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "gpu_oom"
}

// Request is one command. Extra fields go into Args.
type Request map[string]any

// Event is one line from the helper.
type Event struct {
	ID         string            `json:"id"`
	Event      string            `json:"event"`
	Percent    float64           `json:"percent"`
	Files      map[string]string `json:"files"`
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Confidence float64           `json:"confidence"`
	VocalDb    float64           `json:"vocalDb"`
	Protocol   int               `json:"protocol"`
	Devices    []string          `json:"devices"`
}

// Config says how to launch the helper.
type Config struct {
	Command []string
	Dir     string
	Env     []string
	LowPrio bool
	Idle    time.Duration
}

// Client runs one command at a time.
type Client struct {
	cfg Config

	jobMu sync.Mutex // one command at a time

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	events  chan Event
	dead    chan struct{}
	timer   *time.Timer
	devices []string
	lastErr []string
	seq     int
}

// New creates a client. Nothing starts until the first call.
func New(cfg Config) *Client {
	if cfg.Idle == 0 {
		cfg.Idle = IdleShutdown
	}
	return &Client{cfg: cfg}
}

// Running reports whether the helper process is alive.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd != nil
}

// Devices returns what the last ping reported.
func (c *Client) Devices() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.devices
}

func (c *Client) start(ctx context.Context) error {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if c.cmd != nil {
		c.mu.Unlock()
		return nil
	}
	if len(c.cfg.Command) == 0 {
		c.mu.Unlock()
		return errors.New("sidecar command is not configured")
	}
	cmd := exec.Command(c.cfg.Command[0], c.cfg.Command[1:]...)
	tools.ApplyProcAttrs(cmd, c.cfg.LowPrio)
	cmd.Dir = c.cfg.Dir
	cmd.Env = append(cmd.Environ(), c.cfg.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if err := tools.StartLowered(cmd, c.cfg.LowPrio); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("start sidecar: %w", err)
	}
	events := make(chan Event, 64)
	dead := make(chan struct{})
	c.cmd, c.stdin, c.events, c.dead = cmd, stdin, events, dead
	c.mu.Unlock()

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			slog.Debug("sidecar", "line", sc.Text())
			c.mu.Lock()
			c.lastErr = append(c.lastErr, sc.Text())
			if len(c.lastErr) > 8 {
				c.lastErr = c.lastErr[1:]
			}
			c.mu.Unlock()
		}
	}()
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 8<<20)
		for sc.Scan() {
			var e Event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				slog.Warn("sidecar sent a bad line", "line", sc.Text())
				continue
			}
			events <- e
		}
		_ = cmd.Wait()
		close(dead)
		c.mu.Lock()
		if c.cmd == cmd {
			c.cmd = nil
		}
		c.mu.Unlock()
	}()

	ev, err := c.exchange(ctx, Request{"op": "ping"}, nil)
	if err != nil {
		c.Kill()
		return fmt.Errorf("sidecar ping: %w", err)
	}
	if ev.Protocol != ProtocolVersion {
		c.Kill()
		return fmt.Errorf("sidecar protocol %d, need %d", ev.Protocol, ProtocolVersion)
	}
	c.mu.Lock()
	c.devices = ev.Devices
	c.mu.Unlock()
	return nil
}

// Kill stops the helper immediately.
func (c *Client) Kill() {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (c *Client) exchange(ctx context.Context, req Request, progress func(float64)) (*Event, error) {
	c.mu.Lock()
	c.seq++
	id := fmt.Sprintf("r%d", c.seq)
	stdin, events, dead := c.stdin, c.events, c.dead
	c.mu.Unlock()
	req["id"] = id
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("sidecar write: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-dead:
			select {
			case e := <-events:
				if e.ID == id && e.Event == "done" {
					return &e, nil
				}
			default:
			}
			c.mu.Lock()
			tail := strings.Join(c.lastErr, " | ")
			c.mu.Unlock()
			return nil, fmt.Errorf("sidecar exited during a job: %s", tail)
		case e := <-events:
			if e.ID != id {
				continue
			}
			switch e.Event {
			case "progress":
				if progress != nil {
					progress(e.Percent)
				}
			case "done":
				return &e, nil
			case "error":
				return nil, &Error{Code: e.Code, Message: e.Message}
			}
		}
	}
}

// Call sends one command. On a timeout, hang or crash it kills the helper, and
// the job system retries.
func (c *Client) Call(ctx context.Context, req Request, progress func(float64)) (*Event, error) {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()
	if err := c.start(ctx); err != nil {
		return nil, err
	}
	ev, err := c.exchange(ctx, req, progress)
	var se *Error
	if err != nil && !(errors.As(err, &se) && se.Code != "gpu_oom") {
		c.Kill()
	}
	if se != nil && se.Code == "gpu_oom" {
		return nil, err
	}
	c.armIdle()
	return ev, err
}

func (c *Client) armIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return
	}
	c.timer = time.AfterFunc(c.cfg.Idle, func() { c.Shutdown() })
}

// Shutdown asks the helper to exit, returning all of its memory.
func (c *Client) Shutdown() {
	if !c.jobMu.TryLock() {
		return // a job has started
	}
	defer c.jobMu.Unlock()
	c.mu.Lock()
	cmd, stdin, dead := c.cmd, c.stdin, c.dead
	c.mu.Unlock()
	if cmd == nil {
		return
	}
	_, _ = stdin.Write([]byte(`{"id":"bye","op":"shutdown"}` + "\n"))
	select {
	case <-dead:
	case <-time.After(5 * time.Second):
		c.Kill()
	}
}
