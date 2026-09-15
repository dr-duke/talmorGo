package downloader_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestRun_Timeout проверяет, что истёкший таймаут доходит до вызывающего
// ошибкой. Раньше он попадал в ту же ветку, что и отмена пользователем, и
// наверх не уходило ничего: пул видел «ни файлов, ни ошибки» и записывал
// заданию «готово» — с пустой медиатекой и без права на повтор.
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

	done := make(chan struct{})
	var errs []error
	go func() {
		defer close(done)
		for e := range downloader.Run(context.Background(), "https://example.com/slow", opts) {
			if e.Err != nil {
				errs = append(errs, e.Err)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("канал не закрылся: процесс не был остановлен по таймауту")
	}

	if len(errs) == 0 {
		t.Fatal("таймаут не сообщён наверх — задание получило бы статус «готово»")
	}
	if !strings.Contains(errs[0].Error(), "таймаут") {
		t.Errorf("ошибка не опознаётся как таймаут: %v", errs[0])
	}
}

// TestRun_CancelStaysSilent проверяет обратное: отмена вызывающим ошибкой не
// считается, иначе отменённое задание уходило бы в повтор вместо «отменено».
// Отменяем только после фактического запуска процесса — отмена до старта даёт
// законную ошибку "start: context canceled", и пул её не увидит, потому что
// проверяет отмену раньше ветки с ошибкой.
func TestRun_CancelStaysSilent(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	scriptPath := filepath.Join(dir, "slow.sh")
	script := "#!/bin/sh\ntouch " + marker + "\nsleep 60\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := downloader.Run(ctx, "https://example.com/slow", downloader.Options{
		Binary:    scriptPath,
		OutputDir: dir,
		Timeout:   30 * time.Second,
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("процесс так и не стартовал")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	for e := range ch {
		if e.Err != nil {
			t.Errorf("отмена сообщена как ошибка загрузки: %v", e.Err)
		}
	}
}
