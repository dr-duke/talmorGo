package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// NotifKind определяет тип уведомления.
type NotifKind uint8

const (
	NotifJobStarted  NotifKind = iota // задание взято в работу → редактируем сообщение очереди
	NotifFileDone                     // файл скачан → новое сообщение-карточка
	NotifJobDone                      // все файлы готово → удаляем сообщение очереди
	NotifJobFailed                    // задание окончательно упало → редактируем сообщение
	NotifJobRetrying                  // запланирован повтор → редактируем сообщение
)

// Notification — единый тип для всех уведомлений воркера.
type Notification struct {
	Kind      NotifKind
	ChatID    int64
	MessageID int64  // ID сообщения очереди в Telegram (для редактирования/удаления)
	JobID     string // для кнопки Stop/Retry
	JobURL    string // краткий URL для отображения в сообщении очереди
	FileName  string // NotifFileDone: имя файла
	Token     string // NotifFileDone: presigned-токен
	ErrText   string // NotifJobFailed: текст ошибки
	RetryAt   string // NotifJobRetrying: «через Xm»
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
	inFlight  *InFlightPaths

	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc // jobID → cancel активной закачки
}

func NewPool(cfg *config.Config, jobRepo repo.JobRepo, fileRepo repo.FileRepo, tokenRepo repo.TokenRepo, notifier Notifier) *Pool {
	return &Pool{
		cfg:         cfg,
		jobRepo:     jobRepo,
		fileRepo:    fileRepo,
		tokenRepo:   tokenRepo,
		notifier:    notifier,
		notify:      make(chan struct{}, cfg.WorkerCount),
		cancelFuncs: make(map[string]context.CancelFunc),
		inFlight:    NewInFlightPaths(),
	}
}

// InFlight возвращает набор путей, активно обрабатываемых воркерами.
// Используется DirScanner для исключения файлов в процессе закачки.
func (p *Pool) InFlight() *InFlightPaths { return p.inFlight }

// CancelJob прерывает активно скачиваемый job. Возвращает true если job был running.
func (p *Pool) CancelJob(jobID string) bool {
	p.mu.Lock()
	fn, ok := p.cancelFuncs[jobID]
	if ok {
		delete(p.cancelFuncs, jobID)
	}
	p.mu.Unlock()
	if ok {
		fn()
	}
	return ok
}

// moveFile перемещает файл src→dst. Сначала пробует атомарный rename (один ФС),
// при ошибке кросс-устройства — копирование с последующим удалением исходника.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst) //nolint:errcheck
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func (p *Pool) SetNotifier(n Notifier) { p.notifier = n }

func (p *Pool) Enqueue() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *Pool) Start(ctx context.Context) {
	if err := p.jobRepo.ResetStale(ctx); err != nil {
		slog.Error("worker: reset stale jobs", "err", err)
	}
	// Чистим staging от незавершённых загрузок прошлых запусков (после краха).
	if err := os.RemoveAll(p.cfg.StagingDir()); err != nil {
		slog.Warn("worker: clean staging dir", "dir", p.cfg.StagingDir(), "err", err)
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

func (p *Pool) tgJob(job *model.Job) bool {
	return job.Source == "telegram" && job.ChatID != 0 && p.notifier != nil
}

func (p *Pool) process(ctx context.Context, job *model.Job) {
	// Per-job контекст: CancelJob() вызывает cancel() и убивает yt-dlp процесс.
	jobCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancelFuncs[job.ID] = cancel
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.cancelFuncs, job.ID)
		p.mu.Unlock()
		cancel()
	}()

	slog.Info("worker: processing job", "id", job.ID, "url", job.URL, "attempt", job.RetryCount+1)

	if p.tgJob(job) {
		p.notifier.Notify(ctx, Notification{
			Kind:      NotifJobStarted,
			ChatID:    job.ChatID,
			MessageID: job.TgMessageID,
			JobID:     job.ID,
			JobURL:    job.URL,
		})
	}

	// Скачиваем в изолированную per-job staging-папку вне зоны сканирования,
	// затем перемещаем готовый файл в OutputDir. Сканер не видит файл в процессе закачки.
	jobStaging := filepath.Join(p.cfg.StagingDir(), job.ID)
	if err := os.MkdirAll(jobStaging, 0o755); err != nil {
		slog.Error("worker: create staging dir", "dir", jobStaging, "err", err)
		p.handleFailure(ctx, job, fmt.Errorf("create staging dir: %w", err))
		return
	}
	defer os.RemoveAll(jobStaging) // чистим staging при любом исходе

	opts := downloader.Options{
		Binary:       p.cfg.YtDlpBinary,
		OutputDir:    jobStaging,
		OutputFormat: p.cfg.YtDlpOutputFormat,
		Proxy:        p.cfg.YtDlpProxy,
		Timeout:      time.Duration(p.cfg.YtDlpTimeout) * time.Second,
		MaxFiles:     p.cfg.YtDlpMaxFilesPerRequest,
		ExtraArgs:    p.cfg.ExtraArgsList(),
	}

	var firstFile *model.File
	var lastErr error
	fileCount := 0

	for event := range downloader.Run(jobCtx, job.URL, opts) {
		if event.Log != "" {
			if err := p.jobRepo.SaveLog(ctx, job.ID, event.Log); err != nil {
				slog.Warn("worker: save log", "job", job.ID, "err", err)
			}
			continue
		}
		if event.Err != nil {
			lastErr = event.Err
			slog.Error("worker: download event error", "job", job.ID, "err", event.Err)
			continue
		}

		// Перемещаем готовый файл из staging в OutputDir под защитой inFlight:
		// сканер пропускает путь, пока он не окажется в БД.
		finalPath := filepath.Join(p.cfg.YtDlpOutputDir, event.FileName)
		p.inFlight.Add(finalPath)
		if err := moveFile(event.Path, finalPath); err != nil {
			p.inFlight.Remove(finalPath)
			lastErr = fmt.Errorf("move %s: %w", event.FileName, err)
			slog.Error("worker: move file from staging", "src", event.Path, "dst", finalPath, "err", err)
			continue
		}

		info, err := os.Stat(finalPath)
		if err != nil {
			p.inFlight.Remove(finalPath)
			slog.Error("worker: stat file", "path", finalPath, "err", err)
			continue
		}

		f := &model.File{
			JobID: job.ID,
			Path:  finalPath,
			Name:  event.FileName,
			Size:  info.Size(),
		}
		if err := p.fileRepo.Create(ctx, f); err != nil {
			p.inFlight.Remove(finalPath)
			slog.Error("worker: save file record", "err", err)
			continue
		}
		// Путь теперь в БД — AllPaths его видит; убираем из inFlight.
		p.inFlight.Remove(finalPath)
		slog.Info("worker: file saved", "name", f.Name, "id", f.ID)
		fileCount++
		if firstFile == nil {
			firstFile = f
		}

		// Отправляем карточку файла в Telegram сразу после сохранения.
		if p.tgJob(job) && p.tokenRepo != nil {
			if tok, err := p.tokenRepo.Upsert(ctx, f.ID); err == nil {
				p.notifier.Notify(ctx, Notification{
					Kind:     NotifFileDone,
					ChatID:   job.ChatID,
					JobID:    job.ID,
					FileName: f.Name,
					Token:    tok.Token,
				})
			}
		}
	}

	// Если контекст отменён — пользователь нажал «Отменить».
	// Незавершённые файлы лежат в jobStaging и удаляются defer-ом os.RemoveAll.
	if jobCtx.Err() != nil {
		job.Status = model.JobCancelled
		if err := p.jobRepo.Update(ctx, job); err != nil {
			slog.Error("worker: update job cancelled", "err", err)
		}
		slog.Info("worker: job cancelled", "id", job.ID)
		return
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

	// Удаляем сообщение очереди — карточки файлов уже отправлены.
	if p.tgJob(job) {
		p.notifier.Notify(ctx, Notification{
			Kind:      NotifJobDone,
			ChatID:    job.ChatID,
			MessageID: job.TgMessageID,
		})
	}
	slog.Info("worker: job done", "id", job.ID, "title", job.Title)
}

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
		if p.tgJob(job) {
			p.notifier.Notify(ctx, Notification{
				Kind:      NotifJobFailed,
				ChatID:    job.ChatID,
				MessageID: job.TgMessageID,
				JobID:     job.ID,
				JobURL:    job.URL,
				ErrText:   lastErr.Error(),
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
	if p.tgJob(job) {
		retryIn := formatDuration(time.Until(nextRetry))
		p.notifier.Notify(ctx, Notification{
			Kind:      NotifJobRetrying,
			ChatID:    job.ChatID,
			MessageID: job.TgMessageID,
			JobID:     job.ID,
			JobURL:    job.URL,
			RetryAt:   retryIn,
		})
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "скоро"
	}
	if d < time.Hour {
		return fmt.Sprintf("через %dm", int(d.Minutes()))
	}
	return fmt.Sprintf("через %dh", int(d.Hours()))
}
