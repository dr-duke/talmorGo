package repo

import (
	"context"

	"github.com/dr-duke/talmorGo/internal/model"
)

type JobFilter struct {
	Statuses []model.JobStatus
}

type JobRepo interface {
	Create(ctx context.Context, job *model.Job) error
	GetByID(ctx context.Context, id string) (*model.Job, error)
	List(ctx context.Context, f JobFilter) ([]*model.Job, error)
	// ListMedia возвращает объединённое представление заданий + файлов + тегов.
	ListMedia(ctx context.Context) ([]*model.MediaItem, error)
	// SearchMedia ищет по имени файла, URL, домену и тегам (LIKE).
	SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error)
	// LastMedia возвращает последние n успешно скачанных доступных файлов.
	LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error)
	ClaimNext(ctx context.Context) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	Delete(ctx context.Context, id string) error
	ResetFailed(ctx context.Context, id string) error
	ResetStale(ctx context.Context) error
	// Redownload сбрасывает задание в pending и очищает привязку к файлу.
	Redownload(ctx context.Context, id string) error
	// SetTgMessageID сохраняет ID Telegram-сообщения очереди для редактирования/удаления.
	SetTgMessageID(ctx context.Context, jobID string, msgID int64) error
}

type FileRepo interface {
	Create(ctx context.Context, f *model.File) error
	GetByID(ctx context.Context, id string) (*model.File, error)
	List(ctx context.Context) ([]*model.File, error)
	ListAll(ctx context.Context) ([]*model.File, error) // включая удалённые/потерянные
	ListByJobID(ctx context.Context, jobID string) ([]*model.File, error)
	ListDeleted(ctx context.Context) ([]*model.DeletedFile, error)
	Rename(ctx context.Context, id, newName, newPath string) error
	Delete(ctx context.Context, id string) error
	MarkLost(ctx context.Context, id string) error
	MarkFound(ctx context.Context, id string) error
}

type TokenRepo interface {
	Upsert(ctx context.Context, fileID string) (*model.Token, error)
	GetByToken(ctx context.Context, token string) (*model.Token, error)
}

type TagRepo interface {
	Upsert(ctx context.Context, name string) (*model.Tag, error)
	ListAll(ctx context.Context) ([]*model.Tag, error)
	AddToJob(ctx context.Context, jobID, tagID string) error
	RemoveFromJob(ctx context.Context, jobID, tagName string) error
}
