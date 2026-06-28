package downloader

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Event struct {
	// FileName содержит имя файла после успешного скачивания.
	FileName string
	// Path — полный путь к файлу.
	Path string
	Err  error
	// Log — полный вывод stderr (только в финальном событии после завершения процесса).
	Log string
}

type Options struct {
	Binary       string
	OutputDir    string
	OutputFormat string
	Proxy        string
	Timeout      time.Duration
	MaxFiles     int // передаётся в --playlist-items "1:N"; 0 — без лимита
	ExtraArgs    []string
	CookiesFile  string // путь к Netscape cookies.txt; пусто — куки не используются
}

// Run запускает yt-dlp для указанного URL и возвращает канал событий.
// Событие отправляется сразу после завершения обработки каждого файла —
// не нужно ждать завершения всего процесса (важно для плейлистов).
// Канал закрывается после завершения процесса и отправки финального статуса.
func Run(ctx context.Context, url string, opts Options) <-chan Event {
	ch := make(chan Event, 8)
	go func() {
		defer close(ch)

		args := buildArgs(url, opts)
		slog.Info("downloader: start", "binary", opts.Binary, "url", url, "args", args)

		deadline := opts.Timeout
		if deadline <= 0 {
			deadline = 5 * time.Minute
		}
		ctx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()

		cmd := exec.CommandContext(ctx, opts.Binary, args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			ch <- Event{Err: fmt.Errorf("stdout pipe: %w", err)}
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			ch <- Event{Err: fmt.Errorf("stderr pipe: %w", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			ch <- Event{Err: fmt.Errorf("start: %w", err)}
			return
		}

		filePattern := buildFilePattern(opts.OutputDir)

		const maxLogLines = 2000

		var (
			mu        sync.Mutex
			fileCount int
			logLines  []string // полный stderr для хранения в БД
			wg        sync.WaitGroup
		)

		wg.Add(2)

		// stdout содержит только --print-вывод (пути к готовым файлам).
		// Отправляем событие немедленно — не ждём завершения всего процесса.
		go func() {
			defer wg.Done()
			s := bufio.NewScanner(stdout)
			for s.Scan() {
				text := s.Text()
				if filePattern.MatchString(text) {
					mu.Lock()
					fileCount++
					mu.Unlock()
					ch <- Event{FileName: filepath.Base(text), Path: text}
				} else {
					slog.Debug("yt-dlp stdout", "line", text)
				}
			}
		}()

		// stderr: логируем в slog и накапливаем для хранения в БД.
		go func() {
			defer wg.Done()
			s := bufio.NewScanner(stderr)
			for s.Scan() {
				text := s.Text()
				slog.Info("yt-dlp stderr", "line", text)
				mu.Lock()
				if len(logLines) < maxLogLines {
					logLines = append(logLines, text)
				}
				mu.Unlock()
			}
		}()

		wg.Wait()
		cmdErr := cmd.Wait()

		mu.Lock()
		count := fileCount
		fullLog := strings.Join(logLines, "\n")
		errMsg := lastErrorLine(logLines)
		mu.Unlock()

		// Отправляем накопленный лог для сохранения в БД.
		if fullLog != "" {
			ch <- Event{Log: fullLog}
		}

		// Сигнализируем ошибку только если не скачали ни одного файла.
		// При частичном успехе (--no-abort-on-error) файлы уже отправлены выше.
		if cmdErr != nil && ctx.Err() == nil && count == 0 {
			if errMsg == "" {
				errMsg = cmdErr.Error()
			}
			ch <- Event{Err: fmt.Errorf("%s", errMsg)}
		}
	}()
	return ch
}

// lastErrorLine возвращает последнюю строку, начинающуюся с "ERROR:".
func lastErrorLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "ERROR:") {
			return lines[i]
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	return ""
}

func buildArgs(url string, opts Options) []string {
	args := []string{
		"-o", "%(title)s.%(ext)s",
		"--print", "after_move:filename",
		"--no-simulate",
		// Выбираем лучший видео+аудио; фолбек на best combined если раздельных треков нет.
		"-f", "bv*+ba/b",
		"--merge-output-format", opts.OutputFormat,
		"-P", opts.OutputDir,
		// Не прерывать весь job при ошибке одного видео в плейлисте.
		"--no-abort-on-error",
	}
	if opts.MaxFiles > 0 {
		// Ограничиваем на стороне yt-dlp, а не только в Go — экономит трафик.
		args = append(args, "--playlist-items", fmt.Sprintf("1:%d", opts.MaxFiles))
	}
	if opts.Proxy != "" {
		args = append(args, "--proxy", opts.Proxy)
	}
	if opts.CookiesFile != "" {
		args = append(args, "--cookies", opts.CookiesFile)
	}
	args = append(args, opts.ExtraArgs...)
	args = append(args, url)
	return args
}

// buildFilePattern строит regexp для распознавания строк с путём к файлу.
func buildFilePattern(outputDir string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(strings.TrimRight(outputDir, "/") + "/")
	return regexp.MustCompile(`^` + escaped + `.+\.\w{2,5}$`)
}
