# KaraokeDock

Self-hosted karaoke for a home network. One machine prepares and serves the songs, a TV browser shows the lyrics, and guests scan a QR code to queue songs from their phones. There is no app to install and no account to create.

Once a song is prepared, it plays without internet.

## What it does

- Finds songs on YouTube, or takes a pasted link, and keeps only the audio and lyrics
- Removes the vocals when a track has them, and skips that step for karaoke tracks
- Gets lyrics from LRCLIB and song details from MusicBrainz
- Times the lyrics from the colour wipe in karaoke videos, or from the singing with Whisper
- Lets guests add, remove and search songs from their phones
- Gives the host a PIN-protected remote on the phone
- Has an admin page for settings, storage, the library and processing status

## Requirements

- Windows or Linux
- [Go](https://go.dev), at the version in `server/go.mod`
- [Node.js](https://nodejs.org) and [pnpm](https://pnpm.io)
- [uv](https://docs.astral.sh/uv/), which installs its own Python 3.12
- [FFmpeg](https://ffmpeg.org)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) and [Deno](https://deno.com)

A graphics card is optional. AI work runs on the CPU by default.

## Install

Windows:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
```

Linux:

```bash
scripts/setup.sh
```

This installs the Python libraries, downloads the AI models and builds the app. It needs internet and takes a while the first time.

For an Nvidia card, use `-Profile cuda` on Windows or `scripts/setup.sh cuda` on Linux. Reading lyrics from the video only works with the default CPU install.

To add the optional duet lyrics, use `-WithDuetLyrics` on Windows or `--with-duet-lyrics` on Linux.

## Run

Windows:

```powershell
scripts\start.ps1
```

Linux:

```bash
scripts/start.sh
```

Then open these, replacing `<server>` with the address printed at startup:

| Page | Address |
|---|---|
| TV | `http://<server>:7464/tv` |
| Admin | `http://<server>:7464/admin` |
| Phone | Scan the QR code on the TV |

The first start creates an admin password. It's printed once and saved to `data/metadata/initial-admin-password.txt`. You'll choose a new one when you log in, and the file is then deleted.

If you forget the password, run this from the project folder:

```bash
bin/karaokedock admin reset-password
```

On Windows the program is `bin\karaokedock.exe`.

Options: `-port`, `-bind`, `-metadata` and `-sidecar`. The default port is 7464, which spells SING on a phone keypad. `KARAOKEDOCK_PORT` also sets it.

To use your own font for "dock" in the name, save it as `data/metadata/fonts/script.otf`. Without it, a built-in script font is used.

## Development

On Windows, double-click `dev.bat`, or run it from a terminal. It closes any running copy, rebuilds what changed and starts the app.

For live page reloads, keep the app running and start Vite as well:

```bash
cd web
pnpm dev
```

Admin and the TV work through Vite at `/admin.html` and `/tv.html`. The phone page needs the real server for now, because Vite doesn't handle `/r/<room>`.

Tests:

```bash
cd server && go test ./...
cd sidecar && uv run --no-sync pytest -q
cd web && pnpm test
```

## Project layout

| Folder | What's in it |
|---|---|
| `server` | Go backend, job queue and SQLite database code |
| `sidecar` | Python helper for vocal removal, lyric timing and reading text from video |
| `web` | Vue pages for the TV, phone and admin |
| `scripts` | Setup and start scripts |
| `data` | Songs, database and models, created at runtime and not committed |

`GUIDELINES.md` has the full design and the reasons behind it.

## Limits

- Internet is needed for setup, YouTube downloads and lyric lookups.
- It's built for one room on a local network. It isn't meant to be exposed to the internet.
- Phones connect over plain `http`, so they can't use the microphone, and adding the page to a home screen only makes a shortcut.
- YouTube sometimes blocks downloads with a bot check, and those songs can't be added until it clears.
- Only download what you have the right to use.
