package repo

import (
	"context"

	"github.com/dr-duke/talmorGo/internal/model"
)

type JobFilter struct {
	Statuses []model.JobStatus
}

// ItemRepo — единый репозиторий медиаэлементов (видео + аудио).
type ItemRepo interface {
	Create(ctx context.Context, item *model.Item) error
	GetByID(ctx context.Context, id string) (*model.Item, error)
	ListAll(ctx context.Context) ([]*model.Item, error)
	ListByJobID(ctx context.Context, jobID string) ([]*model.Item, error)
	// DeleteAllByJobID удаляет все записи items задания из БД (при redownload).
	DeleteAllByJobID(ctx context.Context, jobID string) error
	ListDeleted(ctx context.Context) ([]*model.DeletedItem, error)
	// AllPaths возвращает множество всех известных путей для сверки при сканировании.
	AllPaths(ctx context.Context) (map[string]struct{}, error)
	// PathsForCleanup возвращает пути элементов failed/hidden заданий (для удаления с диска).
	PathsForCleanup(ctx context.Context) ([]string, error)
	// PruneLost удаляет из БД записи, помеченные как потерянные.
	PruneLost(ctx context.Context) (int, error)
	Rename(ctx context.Context, id, newName, newPath string) error
	SoftDelete(ctx context.Context, id string) error
	MarkLost(ctx context.Context, id string) error
	MarkFound(ctx context.Context, id string) error
	UpdateMeta(ctx context.Context, id string, meta model.AudioMeta) error
	// BulkUpdateMetaFields обновляет только указанные поля (title/artist/album/year/genre)
	// для набора элементов. Ключи, отсутствующие в fields, не затрагиваются.
	BulkUpdateMetaFields(ctx context.Context, ids []string, fields map[string]string) error
}

type CollectionRepo interface {
	List(ctx context.Context) ([]*model.Collection, error)
	Create(ctx context.Context, name string) (*model.Collection, error)
	Delete(ctx context.Context, id string) error
	Rename(ctx context.Context, id, name string) error
	// AddJobs добавляет набор job_id в коллекцию через тег (имя коллекции = имя тега).
	AddJobs(ctx context.Context, collectionID string, jobIDs []string) error
}

type JobRepo interface {
	Create(ctx context.Context, job *model.Job) error
	GetByID(ctx context.Context, id string) (*model.Job, error)
	List(ctx context.Context, f JobFilter) ([]*model.Job, error)
	// ListMedia возвращает объединённое представление заданий + items + тегов.
	ListMedia(ctx context.Context) ([]*model.MediaItem, error)
	// FilterMedia — серверная фильтрация: текст + kind + AND-теги.
	FilterMedia(ctx context.Context, f model.MediaFilter) ([]*model.MediaItem, error)
	// SearchMedia ищет по имени файла, URL, домену и тегам (LIKE, для Telegram).
	SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error)
	// LastMedia возвращает последние n доступных элементов (для Telegram-уведомлений).
	LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error)
	ClaimNext(ctx context.Context) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	Cancel(ctx context.Context, id string) error
	CancelAll(ctx context.Context) (int64, error)
	ConfirmSingle(ctx context.Context, id string) error
	DeleteChecking(ctx context.Context, id string) error
	Hide(ctx context.Context, id string) error
	Unhide(ctx context.Context, id string) error
	Purge(ctx context.Context, id string) error
	CleanupDead(ctx context.Context) (int, error)
	ResetFailed(ctx context.Context, id string) error
	ResetStale(ctx context.Context) error
	Redownload(ctx context.Context, id string) error
	SetTgMessageID(ctx context.Context, jobID string, msgID int64) error
	SaveLog(ctx context.Context, jobID, log string) error
	GetLog(ctx context.Context, jobID string) (string, error)
}

type TokenRepo interface {
	Upsert(ctx context.Context, itemID string) (*model.Token, error)
	GetByToken(ctx context.Context, token string) (*model.Token, error)
}

type TagRepo interface {
	Upsert(ctx context.Context, name string) (*model.Tag, error)
	ListAll(ctx context.Context) ([]*model.Tag, error)
	ListWithCount(ctx context.Context) ([]*model.TagWithCount, error)
	AddToJob(ctx context.Context, jobID, tagID string) error
	BulkAddToJobs(ctx context.Context, tagID string, jobIDs []string) error
	RemoveFromJob(ctx context.Context, jobID, tagName string) error
	// PruneOrphans удаляет оборванные job_tags, пустые теги и пустые коллекции.
	// Возвращает количество удалённых: привязок, тегов, коллекций.
	PruneOrphans(ctx context.Context) (nJobTags, nTags, nCollections int, err error)
}

type SettingsRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	All(ctx context.Context) (map[string]string, error)
}

type CookieRepo interface {
	Upsert(ctx context.Context, domain, content string) error
	List(ctx context.Context) ([]*model.CookieRecord, error)
	Delete(ctx context.Context, domain string) error
	MergeAll(ctx context.Context) (string, error)
}
