package main

import (
	"archive/zip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"karaokedock/internal/tools"
)

// A helper program KaraokeDock needs on Windows. Linux installs these with its
// package manager, and Docker has them in the image.
type helper struct {
	name, url, file string // file is what the download is saved as
	zipped          bool
}

// windowsHelpers are downloaded by the installer (see deploy/windows/setup.iss,
// which uses the same file names) or, failing that, by setupDeps.
var windowsHelpers = []helper{
	{"yt-dlp", "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe", "yt-dlp.exe", false},
	{"deno", "https://github.com/denoland/deno/releases/latest/download/deno-x86_64-pc-windows-msvc.zip", "deno.zip", true},
	{"uv", "https://github.com/astral-sh/uv/releases/latest/download/uv-x86_64-pc-windows-msvc.zip", "uv.zip", true},
	{"ffmpeg", "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip", "ffmpeg.zip", true},
}

// setupDeps puts in place what preparing songs needs besides the app itself:
// on Windows the helper programs, then the Python AI libraries and the AI
// models. Parts already in place are skipped. from is an optional folder of
// helper downloads, named as in windowsHelpers. say reports each step.
func setupDeps(ctx context.Context, sidecarDir, metadataDir, from string, verbose bool, say func(string)) error {
	var errs []error
	if dir := windowsToolsDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, h := range windowsHelpers {
			if _, err := exec.LookPath(h.name); err == nil {
				continue
			}
			if err := installHelper(ctx, h, from, dir, say); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", h.name, err))
			}
		}
	}
	if sidecarDir == "" {
		return errors.Join(append(errs, errors.New("the AI helper folder (sidecar) wasn't found"))...)
	}
	run := func(step string, args []string, env ...string) {
		say(step)
		cmd := exec.CommandContext(ctx, "uv", args...)
		cmd.Dir = sidecarDir
		cmd.Env = append(os.Environ(), env...)
		if verbose {
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		} else {
			tools.ApplyProcAttrs(cmd, false)
		}
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Errorf("%s %w", strings.ToLower(strings.TrimSuffix(step, "…")), err))
		}
	}
	if _, err := os.Stat(filepath.Join(sidecarDir, ".venv")); err != nil {
		run("Installing Python and the AI libraries (this takes a few minutes)…",
			[]string{"sync", "--extra", "cpu", "--python", "3.12", "--managed-python", "--no-dev"},
			"UV_PYTHON_INSTALL_DIR="+filepath.Join(sidecarDir, ".python"))
	}
	models := filepath.Join(metadataDir, "models")
	if _, err := os.Stat(filepath.Join(models, "Kim_Vocal_2.onnx")); err != nil {
		run("Downloading the AI models…",
			[]string{"run", "--no-sync", "--project", sidecarDir, "python", "-m", "karaokedock_ml.fetch_models", models})
	}
	return errors.Join(errs...)
}

// windowsToolsDir is where helper programs go on Windows, or "" elsewhere.
func windowsToolsDir() string {
	if runtime.GOOS != "windows" || os.Getenv("LOCALAPPDATA") == "" {
		return ""
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "KaraokeDock", "tools")
}

// installHelper puts a helper program in dir, from an earlier download in
// from when there is one, else downloading it now.
func installHelper(ctx context.Context, h helper, from, dir string, say func(string)) error {
	src := ""
	if from != "" {
		if p := filepath.Join(from, h.file); fileExists(p) {
			src = p
		}
	}
	if src == "" {
		say("Downloading " + h.name + "…")
		tmp, err := download(ctx, h.url, dir)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		src = tmp
	} else {
		say("Installing " + h.name + "…")
	}
	if !h.zipped {
		return copyFile(src, filepath.Join(dir, h.name+".exe"))
	}
	return unzipPrograms(src, dir, h.name+".exe")
}

func download(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, "dl-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// unzipPrograms takes the named program, and its companions ffprobe and uvx,
// out of a zip, wherever they sit inside it.
func unzipPrograms(zipPath, dir, exeName string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	found := false
	for _, zf := range zr.File {
		base := strings.ToLower(filepath.Base(zf.Name))
		if base != strings.ToLower(exeName) && base != "ffprobe.exe" && base != "uvx.exe" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(dir, filepath.Base(zf.Name)))
		if err == nil {
			_, err = io.Copy(out, rc)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		rc.Close()
		if err != nil {
			return err
		}
		found = found || base == strings.ToLower(exeName)
	}
	if !found {
		return fmt.Errorf("%s wasn't in the download", exeName)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// runSetup is the "karaokedock setup" command. The Windows installer runs it,
// and it can be run again any time to finish or repair an install.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	var o options
	metaEnv := os.Getenv("KARAOKEDOCK_METADATA")
	fs.StringVar(&o.metadata, "metadata", metaEnv, "metadata folder")
	fs.StringVar(&o.sidecarDir, "sidecar", env("KARAOKEDOCK_SIDECAR", ""), "sidecar project folder")
	from := fs.String("from", "", "folder of helper programs already downloaded")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	o.metadataExplicit = o.metadata != ""
	if o.metadata == "" {
		o.metadata = filepath.Join("data", "metadata")
	}
	prepareTrayDefaults(&o) // on Windows: the app's own folders, and its helpers on PATH
	sidecar := findSidecar(o.sidecarDir)
	fmt.Println("Setting up KaraokeDock. This needs internet and can take several minutes.")
	err := setupDeps(context.Background(), sidecar, o.metadata, *from, true, func(s string) { fmt.Println("\n==> " + s) })
	if err != nil {
		return err
	}
	fmt.Println("\nKaraokeDock is ready.")
	return nil
}
