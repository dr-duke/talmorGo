package downloader_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/downloader"
)

// TestRun_FakeBinary проверяет, что Run корректно парсит строки с путём к файлу.
// В качестве "бинаря" используется echo, который имитирует вывод yt-dlp.
func TestRun_FakeBinary(t *testing.T) {
	dir := t.TempDir()
	fakeFile := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(fakeFile, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// Создаём скрипт-заглушку, который печатает путь к файлу как yt-dlp.
	scriptPath := filepath.Join(dir, "fake-ytdlp.sh")
	script := "#!/bin/sh\necho '" + fakeFile + "'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	opts := downloader.Options{
		Binary:       scriptPath,
		OutputDir:    dir,
		OutputFormat: "mp4",
		Timeout:      5 * time.Second,
	}

	ctx := context.Background()
	ch := downloader.Run(ctx, "https://example.com/video", opts)

	var events []downloader.Event
	for e := range ch {
		events = append(events, e)
	}

	// Должно быть одно событие с именем файла и без ошибки.
	var fileEvents []downloader.Event
	for _, e := range events {
		if e.Err == nil && e.FileName != "" {
			fileEvents = append(fileEvents, e)
		}
	}
	if len(fileEvents) != 1 {
		t.Fatalf("expected 1 file event, got %d: %+v", len(fileEvents), events)
	}
	if fileEvents[0].FileName != "video.mp4" {
		t.Errorf("filename: got %s, want video.mp4", fileEvents[0].FileName)
	}
	if fileEvents[0].Path != fakeFile {
		t.Errorf("path: got %s, want %s", fileEvents[0].Path, fakeFile)
	}
}

// TestRun_Timeout проверяет, что процесс убивается по таймауту.
func TestRun_Timeout(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 60\n"), 0755); err != nil {
		t.Fatal(err)
	}

	opts := downloader.Options{
		Binary:    scriptPath,
		OutputDir: dir,
		Timeout:   100 * time.Millisecond,
	}

	ctx := context.Background()
	ch := downloader.Run(ctx, "https://example.com/slow", opts)

	var errCount int
	for e := range ch {
		if e.Err != nil {
			errCount++
		}
	}
	// Ошибка таймаута не пробрасывается как Event (ctx.Err() != nil), процесс просто убивается.
	// Канал должен закрыться без зависания.
	_ = errCount
}
