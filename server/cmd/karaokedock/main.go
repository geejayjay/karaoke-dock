// Command karaokedock runs the KaraokeDock server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"karaokedock/internal/admin"
	"karaokedock/internal/api"
	"karaokedock/internal/jobs"
	"karaokedock/internal/lyrics"
	"karaokedock/internal/media"
	"karaokedock/internal/player"
	"karaokedock/internal/settings"
	"karaokedock/internal/sidecar"
	"karaokedock/internal/store"
	"karaokedock/internal/tools"
	"karaokedock/internal/webui"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type options struct {
	metadata, bind, sidecarDir string
	port                       int
}

func parse(args []string) (options, error) {
	fs := flag.NewFlagSet("karaokedock", flag.ContinueOnError)
	var o options
	port, _ := strconv.Atoi(env("KARAOKEDOCK_PORT", "7464"))
	fs.StringVar(&o.metadata, "metadata", env("KARAOKEDOCK_METADATA", filepath.Join("data", "metadata")), "metadata folder (fast drive)")
	fs.IntVar(&o.port, "port", port, "port")
	fs.StringVar(&o.bind, "bind", env("KARAOKEDOCK_BIND", "0.0.0.0"), "bind address")
	fs.StringVar(&o.sidecarDir, "sidecar", env("KARAOKEDOCK_SIDECAR", ""), "sidecar project folder")
	err := fs.Parse(args)
	return o, err
}

func findSidecar(given string) string {
	if given != "" {
		return mustAbs(given)
	}
	exe, _ := os.Executable()
	for _, base := range []string{".", "..", filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "..")} {
		p := filepath.Join(base, "sidecar")
		if _, err := os.Stat(filepath.Join(p, "pyproject.toml")); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "reset-password" {
		if err := resetPassword(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func openCore(ctx context.Context, o options) (*store.Store, *settings.Manager, error) {
	if err := os.MkdirAll(o.metadata, 0o755); err != nil {
		return nil, nil, err
	}
	db, err := store.Open(filepath.Join(o.metadata, "karaokedock.db"))
	if err != nil {
		return nil, nil, err
	}
	parent := filepath.Dir(mustAbs(o.metadata))
	cfg, err := settings.Load(ctx, db, map[string]string{
		settings.MediaRoot: filepath.Join(parent, "media"),
		settings.TempRoot:  filepath.Join(parent, "tmp"),
	})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, cfg, nil
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func resetPassword(args []string) error {
	o, err := parse(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, cfg, err := openCore(ctx, o)
	if err != nil {
		return err
	}
	defer db.Close()
	pw, err := admin.New(db, cfg, o.metadata).ResetPassword(ctx)
	if err != nil {
		return err
	}
	fmt.Println("New admin password:", pw)
	return nil
}

func run() error {
	o, err := parse(os.Args[1:])
	if err != nil {
		return err
	}
	logs := &api.LogBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logs), &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, cfg, err := openCore(ctx, o)
	if err != nil {
		return err
	}
	defer db.Close()

	adminSvc := admin.New(db, cfg, o.metadata)
	if err := adminSvc.EnsurePassword(ctx); err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Get(settings.TempRoot), 0o755); err != nil {
		return err
	}
	mediaFs := media.NewFs(cfg.Get(settings.MediaRoot))
	encoders := &tools.Encoders{}
	go func() {
		names := encoders.Probe(ctx)
		slog.Info("video encoders ready", "available", names)
	}()

	sidecarDir := findSidecar(o.sidecarDir)
	var sc *sidecar.Client
	{
		lowPrio := cfg.Get(settings.MLDevice) != "cuda"
		env := []string{"HF_HUB_OFFLINE=1", "PYTHONUNBUFFERED=1", "PYTHONUTF8=1"}
		var command []string
		if sidecarDir != "" {
			command = []string{"uv", "run", "--no-sync", "--project", sidecarDir, "python", "-m", "karaokedock_ml"}
		} else {
			slog.Warn("sidecar folder not found, vocal removal is off")
		}
		sc = sidecar.New(sidecar.Config{Command: command, Dir: sidecarDir, Env: env, LowPrio: lowPrio})
	}
	defer sc.Kill()

	amll := lyrics.NewAMLL(filepath.Join(o.metadata, "amll"))
	jm := jobs.New(jobs.Deps{DB: db, Settings: cfg, Media: mediaFs, MetadataDir: o.metadata, Sidecar: sc, Encoders: encoders, AMLL: amll})
	p := player.New(db, cfg, jm, mediaFs, o.port)
	jm.SetNotifier(p)

	restart := func() { restartSelf(stop) }
	srv := &api.Server{DB: db, Settings: cfg, Jobs: jm, Player: p, Media: mediaFs, Admin: adminSvc, Sidecar: sc, Encoders: encoders,
		AMLL: amll, MetadataDir: o.metadata, Web: webui.Files(), Logs: logs, Restart: restart}

	if err := jm.Run(ctx); err != nil {
		return err
	}
	p.Resume(ctx)
	go mediaFs.Watch(ctx, func(bool) { p.BroadcastNetwork(); jm.Wake() })

	addr := net.JoinHostPort(o.bind, strconv.Itoa(o.port))
	ln, err := listenRetry(ctx, addr)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	slog.Info("KaraokeDock is running", "tv", fmt.Sprintf("http://%s:%d/tv", p.LanIP(), o.port), "admin", fmt.Sprintf("http://%s:%d/admin", p.LanIP(), o.port))
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listenRetry waits for the port, which a restarting server may still hold.
func listenRetry(ctx context.Context, addr string) (net.Listener, error) {
	var last error
	for range 40 {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, last
}

func restartSelf(stop func()) {
	exe, err := os.Executable()
	if err != nil {
		slog.Error("restart", "err", err)
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Start(); err != nil {
		slog.Error("restart", "err", err)
		return
	}
	stop()
}
