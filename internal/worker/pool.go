package worker

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// Notification описывает уведомление, отправляемое пользователю Telegram.
// Поля DownloadURL/ViewURL/Token заполняются при успешном скачивании.
// JobID + IsFailure заполняются при ошибке (для кнопки "Повторить").
type Notification struct {
	ChatID      int64
	Text        string
	DownloadURL string // URL кнопки "Скачать" (?download=true)
	ViewURL     string // URL кнопки "Смотреть" (opens in Telegram browser)
	Token       string // presigned token, для кнопки "🔗 Ссылка" (callback)
	JobID       string // ID задания, для кнопки "↩ Повторить" (callback)
	IsFailure   bool   // показывать кнопку Повторить вместо кнопок плеера
}

// Notifier отправляет уведомления пользователю (реализует Telegram-бот).
type Notifier interface {
	Notify(ctx context.Context, n Notification)
}

type Pool struct {
	cfg       *config.Config
	jobRepo   repo.JobRepo
	fileRepo  repo.FileRepo
	tokenRepo repo.TokenRepo
	notifier  Notifier
	notify    chan struct{}
}

func NewPool(cfg *config.Config, jobRepo repo.JobRepo, fileRepo repo.FileRepo, tokenRepo repo.TokenRepo, notifier Notifier) *Pool {
	return &Pool{
		cfg:       cfg,
		jobRepo:   jobRepo,
		fileRepo:  fileRepo,
		tokenRepo: tokenRepo,
		notifier:  notifier,
		notify:    make(chan struct{}, cfg.WorkerCount),
	}
}

// SetNotifier позволяет установить нотификатор после создания пула
// (для разрыва циклической зависимости бот ↔ пул).
func (p *Pool) SetNotifier(n Notifier) {
	p.notifier = n
}

// Enqueue сигнализирует воркерам о новой задаче в очереди.
func (p *Pool) Enqueue() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// Start запускает N воркеров и блокируется до отмены ctx.
func (p *Pool) Start(ctx context.Context) {
	if err := p.jobRepo.ResetStale(ctx); err != nil {
		slog.Error("worker: reset stale jobs", "err", err)
	}

	for i := range p.cfg.WorkerCount {
		go p.runWorker(ctx, i)
	}

	for range p.cfg.WorkerCount {
		p.Enqueue()
	}

	<-ctx.Done()
}

func (p *Pool) runWorker(ctx context.Context, id int) {
	slog.Info("worker: started", "id", id)
	for {
		select {
		case <-ctx.Done():
			slog.Info("worker: stopped", "id", id)
			return
		case <-p.notify:
		case <-time.After(10 * time.Second):
		}

		for {
			job, err := p.jobRepo.ClaimNext(ctx)
			if err != nil {
				slog.Error("worker: claim next", "err", err)
				break
			}
			if job == nil {
				break
			}
			p.process(ctx, job)
		}
	}
}

func (p *Pool) process(ctx context.Context, job *model.Job) {
	slog.Info("worker: processing job", "id", job.ID, "url", job.URL, "attempt", job.RetryCount+1)

	opts := downloader.Options{
		Binary:       p.cfg.YtDlpBinary,
		OutputDir:    p.cfg.YtDlpOutputDir,
		OutputFormat: p.cfg.YtDlpOutputFormat,
		Proxy:        p.cfg.YtDlpProxy,
		Timeout:      time.Duration(p.cfg.YtDlpTimeout) * time.Second,
		ExtraArgs:    p.cfg.ExtraArgsList(),
	}

	var firstFile *model.File
	var lastErr error

	for event := range downloader.Run(ctx, job.URL, opts) {
		if event.Err != nil {
			lastErr = event.Err
			slog.Error("worker: download event error", "job", job.ID, "err", event.Err)
			continue
		}

		info, err := os.Stat(event.Path)
		if err != nil {
			slog.Error("worker: stat file", "path", event.Path, "err", err)
			continue
		}

		f := &model.File{
			Path: event.Path,
			Name: event.FileName,
			Size: info.Size(),
		}
		if err := p.fileRepo.Create(ctx, f); err != nil {
			slog.Error("worker: save file record", "err", err)
			continue
		}
		slog.Info("worker: file saved", "name", f.Name, "id", f.ID)
		if firstFile == nil {
			firstFile = f
		}
	}

	if lastErr != nil && firstFile == nil {
		p.handleFailure(ctx, job, lastErr)
		return
	}

	job.Status = model.JobDone
	if firstFile != nil {
		job.FileID = firstFile.ID
		job.Title = firstFile.Name
	}
	if err := p.jobRepo.Update(ctx, job); err != nil {
		slog.Error("worker: update job done", "err", err)
	}

	if job.Source == "telegram" && job.ChatID != 0 && p.notifier != nil && firstFile != nil {
		n := Notification{
			ChatID: job.ChatID,
			Text:   "✅ Скачано: " + job.Title,
		}
		if p.cfg.BaseURL != "" && p.tokenRepo != nil {
			if tok, err := p.tokenRepo.Upsert(ctx, firstFile.ID); err == nil {
				n.DownloadURL = p.cfg.BaseURL + "/f/" + tok.Token + "?download=true"
				n.ViewURL = p.cfg.BaseURL + "/f/" + tok.Token
				n.Token = tok.Token
			}
		}
		p.notifier.Notify(ctx, n)
	}
	slog.Info("worker: job done", "id", job.ID, "title", job.Title)
}

// handleFailure обрабатывает ошибку скачивания: планирует повтор с backoff или
// помечает задание как failed при истечении максимального окна.
func (p *Pool) handleFailure(ctx context.Context, job *model.Job, lastErr error) {
	maxDuration := time.Duration(p.cfg.RetryMaxDuration) * time.Second
	base := time.Duration(p.cfg.RetryBackoffBase) * time.Second

	now := time.Now()
	firstFailed := now
	if job.FirstFailedAt != nil {
		firstFailed = *job.FirstFailedAt
	}

	retryCount := job.RetryCount + 1
	shift := min(retryCount-1, 15)
	backoff := base * time.Duration(1<<uint(shift))

	maxWindow := firstFailed.Add(maxDuration)

	if now.Add(backoff).After(maxWindow) {
		job.Status = model.JobFailed
		job.Error = lastErr.Error()
		job.RetryCount = retryCount
		job.FirstFailedAt = &firstFailed
		job.NextRetryAt = nil
		if err := p.jobRepo.Update(ctx, job); err != nil {
			slog.Error("worker: update job failed", "err", err)
		}
		slog.Warn("worker: job failed permanently", "id", job.ID, "attempts", retryCount)
		if job.Source == "telegram" && job.ChatID != 0 && p.notifier != nil {
			p.notifier.Notify(ctx, Notification{
				ChatID:    job.ChatID,
				Text:      "❌ Не удалось скачать:\n" + job.DisplayName() + "\n\n" + lastErr.Error(),
				JobID:     job.ID,
				IsFailure: true,
			})
		}
		return
	}

	nextRetry := now.Add(backoff)
	job.Status = model.JobRetrying
	job.Error = lastErr.Error()
	job.RetryCount = retryCount
	job.NextRetryAt = &nextRetry
	job.FirstFailedAt = &firstFailed
	if err := p.jobRepo.Update(ctx, job); err != nil {
		slog.Error("worker: update job retrying", "err", err)
	}
	slog.Info("worker: retry scheduled", "id", job.ID, "attempt", retryCount, "next_retry", nextRetry.Format(time.RFC3339))
}
