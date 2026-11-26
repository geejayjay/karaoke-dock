package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"karaokedock/internal/store"
	"karaokedock/internal/tools"
	"karaokedock/internal/visual"
)

// Cover photos aren't kept in the repository. To give the demo covers, put
// square JPEGs named after each song (neon-skyline.jpg and so on, see the
// song list) in the folder given by -covers. Without them, songs show the
// app's own placeholder art. The README screenshots used free Unsplash photos.

// addCovers sets each song's album cover, as the app does for a downloaded one.
// covers maps a song ID to its cover's file name, without ".jpg".
func addCovers(ctx context.Context, db *store.Store, meta, from string, covers map[string]string) error {
	dir := filepath.Join(meta, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for id, name := range covers {
		song, err := db.GetSong(ctx, id)
		if err != nil {
			return err
		}
		if song.AlbumID == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(from, name+".jpg"))
		if errors.Is(err, os.ErrNotExist) {
			continue // no photo for this song
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dir, song.AlbumID+".jpg")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
		accent, _ := tools.AccentColor(path)
		if _, err := db.SetAlbumCover(ctx, song.AlbumID, "covers/"+song.AlbumID+".jpg", accent); err != nil {
			return err
		}
		if palette, err := visual.PaletteJSON(path); err == nil {
			_ = os.WriteFile(filepath.Join(dir, song.AlbumID+"-palette.json"), palette, 0o644)
		}
	}
	return nil
}
