// Package queue содержит доменные операции очереди загрузок: постановку ссылки,
// разворачивание плейлистов, отмену и перезапуск заданий.
//
// Сервисом пользуются и веб, и Telegram-бот — раньше каждый складывал задания
// по-своему, и бот при этом синхронно ждал yt-dlp прямо в обработчике сообщения.
package queue

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// Pool — исполнитель загрузок.
type Pool interface {
	Enqueue()
	// CancelJob прерывает активную загрузку. true, если задание выполнялось.
	CancelJob(jobID string) bool
}

// ExpandEvent описывает, чем закончилась проверка ссылки на плейлист.
type ExpandEvent struct {
	PlaceholderID string
	URL           string
	Source        string
	ChatID        int64
	IsPlaylist    bool
	PlaylistTitle string
	Created       int
}

// ExpandObserver получает результат разворачивания. Telegram-бот использует его,
// чтобы заменить сообщение «в очереди» сводкой по плейлисту.
type ExpandObserver interface {
	OnExpanded(ctx context.Context, ev ExpandEvent)
}

type Service struct {
	Jobs     repo.JobRepo
	Items    repo.ItemRepo
	Storage  *storage.Storage
	Expander *playlist.Expander
	Pool     Pool
	Settings *settings.Provider
	Hub      *sse.Hub

	observer ExpandObserver

	// resolving — прерыватели идущих проверок на плейлист, по id заготовки.
	// Service собирается литералом, поэтому карта создаётся лениво.
	mu        sync.Mutex
	resolving map[string]context.CancelFunc
}

func (s *Service) SetObserver(o ExpandObserver) { s.observer = o }

func (s *Service) trackResolve(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.resolving == nil {
		s.resolving = make(map[string]context.CancelFunc)
	}
	s.resolving[id] = cancel
	s.mu.Unlock()
}

func (s *Service) untrackResolve(id string) {
	s.mu.Lock()
	delete(s.resolving, id)
	s.mu.Unlock()
}

// cancelResolve прерывает идущую проверку на плейлист. Без неё процесс yt-dlp
// после отмены жил бы до своего дедлайна впустую.
func (s *Service) cancelResolve(id string) {
	s.mu.Lock()
	fn, ok := s.resolving[id]
	delete(s.resolving, id)
	s.mu.Unlock()
	if ok {
		fn()
	}
}

// ErrInvalidURL — строка не является ссылкой.
var ErrInvalidURL = fmt.Errorf("invalid url")

// Prepare создаёт задание-заготовку в статусе checking. Воркер такие задания
// игнорирует, поэтому вызывающий успевает, например, отправить сообщение в
// Telegram и сохранить его id до старта загрузки.
func (s *Service) Prepare(ctx context.Context, rawURL, source string, chatID int64) (*model.Job, error) {
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return nil, ErrInvalidURL
	}
	job := &model.Job{URL: rawURL, Status: model.JobChecking, Source: source, ChatID: chatID}
	if err := s.Jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// Resolve асинхронно разворачивает заготовку: одиночная ссылка переходит в
// pending, плейлист превращается в набор отдельных заданий.
func (s *Service) Resolve(job *model.Job) {
	opts := s.Settings.ProbeOptions(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	s.trackResolve(job.ID, cancel)
	go func() {
		defer func() {
			s.untrackResolve(job.ID)
			cancel()
		}()
		res := s.Expander.ResolvePlaceholder(ctx, job.ID, job.URL, opts, job.Source, job.ChatID)
		s.Pool.Enqueue()

		if s.observer != nil {
			s.observer.OnExpanded(ctx, ExpandEvent{
				PlaceholderID: job.ID,
				URL:           job.URL,
				Source:        job.Source,
				ChatID:        job.ChatID,
				IsPlaylist:    res.IsPlaylist,
				PlaylistTitle: res.PlaylistTitle,
				Created:       res.Created,
			})
		}
	}()
}

// Add ставит ссылку в очередь и сразу возвращает управление.
func (s *Service) Add(ctx context.Context, rawURL, source string, chatID int64) (*model.Job, error) {
	job, err := s.Prepare(ctx, rawURL, source, chatID)
	if err != nil {
		return nil, err
	}
	s.Resolve(job)
	return job, nil
}

// RecoverChecking перезапускает разворачивание заданий, оборванных рестартом.
// Без этого они висели бы в статусе checking, а перевод их в pending скачал бы
// плейлист одним заданием.
func (s *Service) RecoverChecking(ctx context.Context) {
	jobs, err := s.Jobs.ListChecking(ctx)
	if err != nil {
		slog.Error("queue: list checking jobs", "err", err)
		return
	}
	for _, job := range jobs {
		slog.Info("queue: resuming playlist check", "job", job.ID, "url", job.URL)
		s.Resolve(job)
	}
}

// Cancel отменяет задание: активную загрузку прерываем вместе с процессом,
// остальные помечаем отменёнными в БД.
func (s *Service) Cancel(ctx context.Context, id string) error {
	if s.Pool.CancelJob(id) {
		return nil
	}
	// Порядок важен: сначала фиксируем отмену в БД. ConfirmSingle и
	// DeleteChecking работают только по строке в статусе checking, поэтому
	// после этого разворачивание уже ничего не создаст, даже если успеет
	// дочитать ответ. Раньше отмена заготовки лишь меняла статус, а фоновое
	// разворачивание шло своим чередом и заводило сотню заданий вопреки ей.
	err := s.Jobs.Cancel(ctx, id)
	s.cancelResolve(id)
	return err
}

// CancelAll отменяет все активные задания, включая идущие сейчас загрузки:
// раньше массовая отмена не трогала процессы yt-dlp и они дописывали файлы.
func (s *Service) CancelAll(ctx context.Context) (int, error) {
	ids, err := s.Jobs.ListIDsByStatus(ctx, model.JobRunning)
	if err != nil {
		slog.Warn("queue: list running jobs", "err", err)
	}
	for _, id := range ids {
		s.Pool.CancelJob(id)
	}
	n, err := s.Jobs.CancelAll(ctx)
	if err != nil {
		return 0, err
	}
	s.broadcast()
	return int(n), nil
}

// Retry возвращает упавшее задание в очередь.
func (s *Service) Retry(ctx context.Context, id string) error {
	if err := s.Jobs.ResetFailed(ctx, id); err != nil {
		return err
	}
	s.Pool.Enqueue()
	return nil
}

// Redownload удаляет скачанные файлы задания и ставит ссылку в очередь заново.
func (s *Service) Redownload(ctx context.Context, id string) error {
	job, err := s.Jobs.GetByID(ctx, id)
	if err != nil {
		return err
	}
	items, err := s.Items.ListByJobID(ctx, id)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Storage.Delete(item.Path); err != nil {
			slog.Warn("queue: delete file for redownload", "path", item.Path, "err", err)
		}
	}
	if err := s.Items.DeleteAllByJobID(ctx, id); err != nil {
		return err
	}
	if err := s.Jobs.Redownload(ctx, id); err != nil {
		return err
	}
	job.Status = model.JobChecking
	s.Resolve(job)
	return nil
}

// List возвращает задания, которые показываются в очереди.
func (s *Service) List(ctx context.Context) ([]*model.Job, error) {
	return s.Jobs.List(ctx, repo.JobFilter{
		Statuses: []model.JobStatus{
			model.JobChecking, model.JobPending, model.JobRunning,
			model.JobRetrying, model.JobFailed, model.JobCancelled,
		},
	})
}

func (s *Service) broadcast() {
	if s.Hub != nil {
		s.Hub.Publish(sse.TopicQueue, sse.TopicLibrary)
	}
}
