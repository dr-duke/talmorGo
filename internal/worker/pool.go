package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
)

type NotifKind uint8

const (
	NotifJobStarted  NotifKind = iota
	NotifFileDone
	NotifJobDone
	NotifJobFailed
	NotifJobRetrying
)

type Notification struct {
	Kind      NotifKind
	ChatID    int64
	MessageID int64
	JobID     string
	JobURL    string
	FileName  string
	Token     string
	ErrText   string
	RetryAt   string
}

type Notifier interface {
	Notify(ctx context.Context, n Notification)
}

type Pool struct {
	cfg          *config.Config
	jobRepo      repo.JobRepo
	itemRepo     repo.ItemRepo
	tokenRepo    repo.TokenRepo
	settingsRepo repo.SettingsRepo
	notifier     Notifier
	notify       chan struct{}
	inFlight     *InFlightPaths
	hub          *sse.Hub

	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc
}

func NewPool(cfg *config.Config, jobRepo repo.JobRepo, itemRepo repo.ItemRepo, tokenRepo repo.TokenRepo, notifier Notifier) *Pool {
	return &Pool{
		cfg:         cfg,
		jobRepo:     jobRepo,
		itemRepo:    itemRepo,
		tokenRepo:   tokenRepo,
		notifier:    notifier,
		notify:      make(chan struct{}, cfg.WorkerCount),
		cancelFuncs: make(map[string]context.CancelFunc),
		inFlight:    NewInFlightPaths(),
	}
}

func (p *Pool) SetHub(h *sse.Hub)                    { p.hub = h }
func (p *Pool) SetSettingsRepo(sr repo.SettingsRepo) { p.settingsRepo = sr }

func (p *Pool) broadcast() {
	if p.hub != nil {
		p.hub.Broadcast()
	}
}

func (p *Pool) InFlight() *InFlightPaths { return p.inFlight }

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
	jobCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancelFuncs[job.ID] = cancel
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.cancelFuncs, job.ID)
		p.mu.Unlock()
		cancel()
		p.broadcast()
	}()

	slog.Info("worker: processing job", "id", job.ID, "url", job.URL, "attempt", job.RetryCount+1)
	p.broadcast()

	if p.tgJob(job) {
		p.notifier.Notify(ctx, Notification{
			Kind:      NotifJobStarted,
			ChatID:    job.ChatID,
			MessageID: job.TgMessageID,
			JobID:     job.ID,
			JobURL:    job.URL,
		})
	}

	jobStaging := filepath.Join(p.cfg.StagingDir(), job.ID)
	if err := os.MkdirAll(jobStaging, 0o755); err != nil {
		slog.Error("worker: create staging dir", "dir", jobStaging, "err", err)
		p.handleFailure(ctx, job, fmt.Errorf("create staging dir: %w", err))
		return
	}
	defer os.RemoveAll(jobStaging)

	opts := p.resolveOpts(ctx, jobStaging)

	var firstItem *model.Item
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

		item := &model.Item{
			JobID: job.ID,
			Kind:  "video",
			Path:  finalPath,
			Name:  event.FileName,
			Size:  info.Size(),
		}
		if err := p.itemRepo.Create(ctx, item); err != nil {
			p.inFlight.Remove(finalPath)
			slog.Error("worker: save item record", "err", err)
			continue
		}
		p.inFlight.Remove(finalPath)
		slog.Info("worker: item saved", "name", item.Name, "id", item.ID)
		p.broadcast()
		fileCount++
		if firstItem == nil {
			firstItem = item
		}

		if p.tgJob(job) && p.tokenRepo != nil {
			if tok, err := p.tokenRepo.Upsert(ctx, item.ID); err == nil {
				p.notifier.Notify(ctx, Notification{
					Kind:     NotifFileDone,
					ChatID:   job.ChatID,
					JobID:    job.ID,
					FileName: item.Name,
					Token:    tok.Token,
				})
			}
		}
	}

	if jobCtx.Err() != nil {
		job.Status = model.JobCancelled
		if err := p.jobRepo.Update(ctx, job); err != nil {
			slog.Error("worker: update job cancelled", "err", err)
		}
		slog.Info("worker: job cancelled", "id", job.ID)
		return
	}

	if lastErr != nil && firstItem == nil {
		p.handleFailure(ctx, job, lastErr)
		return
	}

	job.Status = model.JobDone
	if firstItem != nil {
		job.Title = firstItem.Name
	}
	if err := p.jobRepo.Update(ctx, job); err != nil {
		slog.Error("worker: update job done", "err", err)
	}

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

func (p *Pool) resolveOpts(ctx context.Context, outputDir string) downloader.Options {
	proxy := p.cfg.YtDlpProxy
	outputFormat := p.cfg.YtDlpOutputFormat
	maxFiles := p.cfg.YtDlpMaxFilesPerRequest
	timeout := time.Duration(p.cfg.YtDlpTimeout) * time.Second
	extraArgs := p.cfg.ExtraArgsList()

	if p.settingsRepo != nil {
		if v, _ := p.settingsRepo.Get(ctx, "yt_dlp_proxy"); v != "" {
			proxy = v
		}
		if v, _ := p.settingsRepo.Get(ctx, "yt_dlp_output_format"); v != "" {
			outputFormat = v
		}
		if v, _ := p.settingsRepo.Get(ctx, "yt_dlp_max_files"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				maxFiles = n
			}
		}
		if v, _ := p.settingsRepo.Get(ctx, "yt_dlp_timeout"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				timeout = time.Duration(n) * time.Second
			}
		}
		if v, _ := p.settingsRepo.Get(ctx, "yt_dlp_extra_args"); v != "" {
			extraArgs = strings.Fields(v)
		}
	}

	cookiesFile := ""
	if cf := p.cfg.CookiesFilePath(); fileExists(cf) {
		cookiesFile = cf
	}

	return downloader.Options{
		Binary:       p.cfg.YtDlpBinary,
		OutputDir:    outputDir,
		OutputFormat: outputFormat,
		Proxy:        proxy,
		Timeout:      timeout,
		MaxFiles:     maxFiles,
		ExtraArgs:    extraArgs,
		CookiesFile:  cookiesFile,
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
