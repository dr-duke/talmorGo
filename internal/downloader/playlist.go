package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// PlaylistEntry — одно видео из плейлиста.
type PlaylistEntry struct {
	URL   string
	Title string
}

// PlaylistInfo — результат разворачивания плейлиста.
type PlaylistInfo struct {
	Entries       []PlaylistEntry
	PlaylistTitle string // пустая строка, если не определён
}

// FetchPlaylist извлекает список видео из URL без скачивания (--flat-playlist).
// Возвращает nil если URL — одиночное видео, плейлист недоступен или запрос завершился с ошибкой;
// вызывающий должен обработать nil как «создать один job с оригинальным URL».
func FetchPlaylist(ctx context.Context, url string, opts Options) *PlaylistInfo {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	args := []string{
		"--flat-playlist", "--simulate",
		// Три строки на каждое видео: url, title, playlist_title
		"--print", "%(url)s",
		"--print", "%(title)s",
		"--print", "%(playlist_title)s",
	}
	if opts.MaxFiles > 0 {
		args = append(args, "--playlist-items", fmt.Sprintf("1:%d", opts.MaxFiles))
	}
	if opts.Proxy != "" {
		args = append(args, "--proxy", opts.Proxy)
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, opts.Binary, args...)
	out, err := cmd.Output()
	if err != nil {
		slog.Debug("downloader: flat-playlist failed", "url", url, "err", err)
		return nil
	}

	raw := strings.TrimRight(string(out), "\n")
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")

	var entries []PlaylistEntry
	var playlistTitle string

	// Парсим тройками: [url, title, playlist_title]
	for i := 0; i+2 < len(lines); i += 3 {
		entryURL := strings.TrimSpace(lines[i])
		entryTitle := strings.TrimSpace(lines[i+1])
		pTitle := strings.TrimSpace(lines[i+2])

		if entryURL == "" || entryURL == "NA" {
			continue // одиночное видео без JS-runtime вернёт NA
		}
		if entryTitle == "NA" {
			entryTitle = ""
		}
		entries = append(entries, PlaylistEntry{URL: entryURL, Title: entryTitle})
		if playlistTitle == "" && pTitle != "" && pTitle != "NA" {
			playlistTitle = pTitle
		}
	}

	if len(entries) <= 1 {
		return nil // одиночное видео → fallback
	}

	slog.Info("downloader: playlist expanded", "url", url, "entries", len(entries), "title", playlistTitle)
	return &PlaylistInfo{
		Entries:       entries,
		PlaylistTitle: playlistTitle,
	}
}
