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
	"runtime/debug"
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

// defaultTray is set to "true" at link time for the Windows desktop/MSI build.
var defaultTray = "false"

// version is set at link time via -ldflags "-X main.version=..." from the Git tag.
var version = "dev"

func resolveVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev-" + s.Value[:7]
			}
		}
	}
	return "dev"
}

type options struct {
	metadata, bind, sidecarDir string
	port                       int
	tray, noOpen, showVersion  bool
	metadataExplicit           bool
}

func parse(args []string) (options, error) {
	fs := flag.NewFlagSet("karaokedock", flag.ContinueOnError)
	var o options
	port, _ := strconv.Atoi(env("KARAOKEDOCK_PORT", "7464"))
	metaEnv := os.Getenv("KARAOKEDOCK_METADATA")
	defaultMeta := metaEnv
	if defaultMeta == "" {
		defaultMeta = filepath.Join("data", "metadata")
	}
	fs.StringVar(&o.metadata, "metadata", defaultMeta, "metadata folder (fast drive)")
	fs.IntVar(&o.port, "port", port, "port")
	fs.StringVar(&o.bind, "bind", env("KARAOKEDOCK_BIND", "0.0.0.0"), "bind address")
	fs.StringVar(&o.sidecarDir, "sidecar", env("KARAOKEDOCK_SIDECAR", ""), "sidecar project folder")
	fs.BoolVar(&o.tray, "tray", defaultTray == "true", "run in system tray and open Admin")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open browser automatically in tray mode")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	err := fs.Parse(args)
	o.metadataExplicit = metaEnv != ""
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "metadata" {
			o.metadataExplicit = true
		}
	})
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
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(resolveVersion())
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "setup" {
		if err := runSetup(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "\nSetup didn't finish:", err)
			os.Exit(1)
		}
		return
	}
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
	if err := admin.New(db, cfg, o.metadata).ResetPassword(ctx); err != nil {
		return err
	}
	fmt.Println("Admin password removed. Open the admin page to create a new one.")
	return nil
}

func run() error {
	o, err := parse(os.Args[1:])
	if err != nil {
		return err
	}
	if o.showVersion {
		fmt.Println(resolveVersion())
		return nil
	}
	if o.tray {
		prepareTrayDefaults(&o)
		if openExistingAdmin(o.port) {
			return nil
		}
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
	if err := adminSvc.Prepare(ctx); err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Get(settings.TempRoot), 0o755); err != nil {
		return err
	}
	mediaFs := media.NewFs(cfg.Get(settings.MediaRoot))
	encoders := &tools.Encoders{}

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
	appVer := resolveVersion()
	srv := &api.Server{DB: db, Settings: cfg, Jobs: jm, Player: p, Media: mediaFs, Admin: adminSvc, Sidecar: sc, Encoders: encoders,
		AMLL: amll, MetadataDir: o.metadata, Web: webui.Files(), Logs: logs, Restart: restart, Version: appVer}

	addr := net.JoinHostPort(o.bind, strconv.Itoa(o.port))
	ln, err := listenRetry(ctx, addr)
	if err != nil {
		return err
	}

	adminURL := fmt.Sprintf("http://localhost:%d/admin", o.port)
	tvURL := fmt.Sprintf("http://localhost:%d/tv", o.port)
	dataRoot := filepath.Dir(mustAbs(o.metadata))

	if o.tray {
		startTray(ctx, stop, trayLinks{AdminURL: adminURL, TVURL: tvURL, DataDir: dataRoot})
		if !o.noOpen {
			openBrowser(adminURL)
		}
		go func() {
			bootstrapDesktopDeps(ctx, sidecarDir, o.metadata)
			names := encoders.Probe(ctx)
			slog.Info("video encoders ready", "available", names)
			if err := jm.Run(ctx); err != nil {
				slog.Error("job manager", "err", err)
			}
		}()
	} else {
		go func() {
			names := encoders.Probe(ctx)
			slog.Info("video encoders ready", "available", names)
		}()
		if err := jm.Run(ctx); err != nil {
			return err
		}
	}

	p.Resume(ctx)
	go mediaFs.Watch(ctx, func(bool) { p.BroadcastNetwork(); jm.Wake() })

	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	slog.Info("KaraokeDock is running", "version", appVer, "tv", fmt.Sprintf("http://%s:%d/tv", p.LanIP(), o.port), "admin", fmt.Sprintf("http://%s:%d/admin", p.LanIP(), o.port))
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
