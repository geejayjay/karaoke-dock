package sidecar

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain lets this test binary act as a fake helper.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_SIDECAR"); mode != "" {
		fakeSidecar(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeSidecar(mode string) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var r map[string]any
		_ = json.Unmarshal(sc.Bytes(), &r)
		id := r["id"]
		switch r["op"] {
		case "ping":
			proto := 1
			if mode == "oldproto" {
				proto = 99
			}
			fmt.Printf(`{"id":%q,"event":"done","protocol":%d,"devices":["cpu"]}`+"\n", id, proto)
		case "shutdown":
			os.Exit(0)
		default:
			switch mode {
			case "crash":
				os.Exit(3)
			case "hang":
				time.Sleep(time.Hour)
			case "oom":
				fmt.Printf(`{"id":%q,"event":"error","code":"gpu_oom","message":"out of memory"}`+"\n", id)
			default:
				fmt.Printf(`{"id":%q,"event":"progress","percent":50}`+"\n", id)
				fmt.Printf(`{"id":%q,"event":"done","files":{"a":"b"}}`+"\n", id)
			}
		}
	}
}

func client(t *testing.T, mode string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(Config{Command: []string{exe}, Env: []string{"FAKE_SIDECAR=" + mode}, Idle: 50 * time.Millisecond})
	t.Cleanup(c.Kill)
	return c
}

func TestCallAndIdleShutdown(t *testing.T) {
	c := client(t, "ok")
	var seen float64
	ev, err := c.Call(context.Background(), Request{"op": "separate"}, func(p float64) { seen = p })
	if err != nil || ev.Files["a"] != "b" || seen != 50 {
		t.Fatalf("%+v %v %v", ev, err, seen)
	}
	if !c.Running() {
		t.Fatal("should stay running between jobs")
	}
	deadline := time.Now().Add(6 * time.Second)
	for c.Running() {
		if time.Now().After(deadline) {
			t.Fatal("did not stop when idle")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCrashThenRestart(t *testing.T) {
	c := client(t, "crash")
	if _, err := c.Call(context.Background(), Request{"op": "separate"}, nil); err == nil {
		t.Fatal("expected a crash")
	}
	if c.Running() {
		t.Fatal("crashed helper should be gone")
	}
	c.cfg.Env = []string{"FAKE_SIDECAR=ok"}
	if _, err := c.Call(context.Background(), Request{"op": "separate"}, nil); err != nil {
		t.Fatal("restart failed:", err)
	}
}

func TestHangIsKilled(t *testing.T) {
	c := client(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, Request{"op": "separate"}, nil); err == nil {
		t.Fatal("expected a timeout")
	}
	if c.Running() {
		t.Fatal("hung helper should be killed")
	}
}

func TestGPUOOM(t *testing.T) {
	c := client(t, "oom")
	_, err := c.Call(context.Background(), Request{"op": "separate"}, nil)
	if !IsGPUOOM(err) {
		t.Fatal(err)
	}
}

func TestProtocolMismatch(t *testing.T) {
	c := client(t, "oldproto")
	if _, err := c.Call(context.Background(), Request{"op": "separate"}, nil); err == nil {
		t.Fatal("expected mismatch error")
	}
}
