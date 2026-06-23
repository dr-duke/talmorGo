package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
}

type Options struct {
	Binary       string
	OutputDir    string
	OutputFormat string
	Proxy        string
	Timeout      time.Duration
	ExtraArgs    []string
}

// Run запускает yt-dlp для указанного URL и возвращает канал событий.
// Канал закрывается после полного завершения процесса (cmd.Wait() вернулся),
// что гарантирует, что файлы на диске уже записаны.
func Run(ctx context.Context, url string, opts Options) <-chan Event {
	ch := make(chan Event, 4)
	go func() {
		defer close(ch)

		args := buildArgs(url, opts)
		slog.Info("downloader: start", "binary", opts.Binary, "url", url)

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

		var (
			mu         sync.Mutex
			foundPaths []string
			errLines   []string // последние строки stderr для диагностики
			wg         sync.WaitGroup
		)

		scanReader := func(r io.Reader, isStdout bool) {
			defer wg.Done()
			s := bufio.NewScanner(r)
			for s.Scan() {
				text := s.Text()
				if isStdout {
					if filePattern.MatchString(text) {
						mu.Lock()
						foundPaths = append(foundPaths, text)
						mu.Unlock()
					} else {
						slog.Debug("yt-dlp stdout", "line", text)
					}
				} else {
					// stderr логируем видимо, чтобы ошибки не терялись
					slog.Info("yt-dlp stderr", "line", text)
					mu.Lock()
					errLines = append(errLines, text)
					// держим только последние 10 строк
					if len(errLines) > 10 {
						errLines = errLines[len(errLines)-10:]
					}
					mu.Unlock()
				}
			}
		}

		wg.Add(2)
		go scanReader(stdout, true)
		go scanReader(stderr, false)

		wg.Wait()
		cmdErr := cmd.Wait()

		// Процесс завершён и все файлы закрыты — отправляем события.
		for _, path := range foundPaths {
			ch <- Event{FileName: filepath.Base(path), Path: path}
		}

		if cmdErr != nil && ctx.Err() == nil && len(foundPaths) == 0 {
			// Берём последнюю строку ошибки из stderr для понятного сообщения.
			mu.Lock()
			errMsg := lastErrorLine(errLines)
			mu.Unlock()
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
		"--print", "post_process:filename",
		"--no-simulate",
		"--merge-output-format", opts.OutputFormat,
		"-P", opts.OutputDir,
	}
	if opts.Proxy != "" {
		args = append(args, "--proxy", opts.Proxy)
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
