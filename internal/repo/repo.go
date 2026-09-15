package repo

import (
	"context"
	"time"

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
	// KnownPaths возвращает известные пути и признак доступности элемента.
	// Сканеру важно различать их: файл, вернувшийся на место удалённого,
	// нужно не пропустить, а восстановить.
	KnownPaths(ctx context.Context) (map[string]bool, error)
	// RestoreByPath снимает пометки об удалении и пропаже с элемента по пути.
	// Возвращает false, если такого элемента нет.
	RestoreByPath(ctx context.Context, path string, size int64) (bool, error)
	// PathsForCleanup возвращает пути элементов failed/hidden заданий (для удаления с диска).
	PathsForCleanup(ctx context.Context) ([]string, error)
	// PruneLost удаляет из БД записи, помеченные как потерянные.
	PruneLost(ctx context.Context) (int, error)
	Rename(ctx context.Context, id, newName, newPath string) error
	SoftDelete(ctx context.Context, id string) error
	MarkLost(ctx context.Context, id string) error
	MarkFound(ctx context.Context, id string) error
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
	// ListIDsByStatus возвращает id заданий в указанных статусах (для массовой отмены).
	ListIDsByStatus(ctx context.Context, statuses ...model.JobStatus) ([]string, error)
	// FilterMedia — серверная фильтрация медиатеки: текст + kind + AND-теги + пагинация.
	FilterMedia(ctx context.Context, f model.MediaFilter) ([]*model.MediaItem, error)
	// CountMedia — полный счётчик без учёта Limit/Offset (для отображения «X из N»).
	CountMedia(ctx context.Context, f model.MediaFilter) (int, error)
	// SearchMedia ищет по имени файла, URL, заголовку и тегам (для Telegram).
	SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error)
	// LastMedia возвращает последние n доступных элементов (для Telegram-уведомлений).
	LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error)
	ClaimNext(ctx context.Context) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	Cancel(ctx context.Context, id string) error
	CancelAll(ctx context.Context) (int64, error)
	ConfirmSingle(ctx context.Context, id string) error
	// DeleteChecking удаляет заготовку, только пока она в статусе checking.
	// Признак удаления показывает, не отменил ли пользователь задание, пока шла
	// проверка: если строки уже нет, разворачивать плейлист не нужно.
	DeleteChecking(ctx context.Context, id string) (bool, error)
	// ListChecking возвращает задания, зависшие на проверке плейлиста после рестарта.
	ListChecking(ctx context.Context) ([]*model.Job, error)
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
	// DeleteByItemID отзывает постоянную ссылку на элемент.
	DeleteByItemID(ctx context.Context, itemID string) (bool, error)
}

type TagRepo interface {
	Upsert(ctx context.Context, name string) (*model.Tag, error)
	ListAll(ctx context.Context) ([]*model.Tag, error)
	// ListWithCountFiltered возвращает теги с количеством заданий, соответствующих фильтру.
	// Пустой фильтр даёт полное облако тегов.
	ListWithCountFiltered(ctx context.Context, f model.MediaFilter) ([]*model.TagWithCount, error)
	AddToJob(ctx context.Context, jobID, tagID string) error
	BulkAddToJobs(ctx context.Context, tagID string, jobIDs []string) error
	RemoveFromJob(ctx context.Context, jobID, tagName string) error
	// PruneOrphans удаляет оборванные job_tags и пустые теги. Сами коллекции не
	// трогает: это пользовательская сущность, и пустая подборка — не мусор.
	// Возвращает количество удалённых: привязок, тегов, коллекций (всегда 0).
	PruneOrphans(ctx context.Context) (nJobTags, nTags, nCollections int, err error)
}

type OperationRepo interface {
	Create(ctx context.Context, op *model.Operation) error
	GetByID(ctx context.Context, id string) (*model.Operation, error)
	// ClaimNext атомарно переводит старейшую pending-операцию указанных видов
	// в running и возвращает её. Возвращает nil, nil если очередь пуста.
	ClaimNext(ctx context.Context, kinds []string) (*model.Operation, error)
	SetDone(ctx context.Context, id string) error
	SetFailed(ctx context.Context, id, errMsg string) error
	// List возвращает операции заданных видов. Пустой kinds — вернуть все.
	List(ctx context.Context, kinds []string) ([]*model.Operation, error)
	Delete(ctx context.Context, id string) error
	// ResetStale возвращает в очередь операции, оборванные рестартом.
	ResetStale(ctx context.Context) (int, error)
	// DeleteFinishedBefore удаляет завершённые операции старше указанного момента.
	DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error)
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
