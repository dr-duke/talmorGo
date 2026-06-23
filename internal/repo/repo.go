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
	// ClaimNext атомарно берёт одну pending/retrying-задачу и переводит её в running.
	// Возвращает nil, nil если нет готовых к выполнению задач.
	ClaimNext(ctx context.Context) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	Delete(ctx context.Context, id string) error
	// ResetFailed переводит failed-задачу обратно в pending (ручной сброс из веба).
	ResetFailed(ctx context.Context, id string) error
	// ResetStale переводит все running → pending (crash recovery при старте).
	ResetStale(ctx context.Context) error
}

type FileRepo interface {
	Create(ctx context.Context, f *model.File) error
	GetByID(ctx context.Context, id string) (*model.File, error)
	List(ctx context.Context) ([]*model.File, error)
	// ListDeleted возвращает файлы, удалённые с диска, с исходным URL.
	ListDeleted(ctx context.Context) ([]*model.DeletedFile, error)
	Rename(ctx context.Context, id, newName, newPath string) error
	// Delete — мягкое удаление: файл остаётся в БД с deleted_at = now().
	Delete(ctx context.Context, id string) error
}

type TokenRepo interface {
	// Upsert возвращает существующий токен для файла или создаёт новый.
	Upsert(ctx context.Context, fileID string) (*model.Token, error)
	GetByToken(ctx context.Context, token string) (*model.Token, error)
}
