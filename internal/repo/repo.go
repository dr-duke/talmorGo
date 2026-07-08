package repo

import (
	"context"

	"github.com/dr-duke/talmorGo/internal/model"
)

type JobFilter struct {
	Statuses []model.JobStatus
}

type CollectionRepo interface {
	List(ctx context.Context) ([]*model.Collection, error)
	Create(ctx context.Context, name string) (*model.Collection, error)
	Delete(ctx context.Context, id string) error
	Rename(ctx context.Context, id, name string) error
	// AddJobs добавляет набор job_id в коллекцию (INSERT OR IGNORE).
	AddJobs(ctx context.Context, collectionID string, jobIDs []string) error
	// RemoveJob убирает одно задание из коллекции.
	RemoveJob(ctx context.Context, collectionID, jobID string) error
}

type JobRepo interface {
	Create(ctx context.Context, job *model.Job) error
	GetByID(ctx context.Context, id string) (*model.Job, error)
	List(ctx context.Context, f JobFilter) ([]*model.Job, error)
	// ListMedia возвращает объединённое представление заданий + файлов + тегов.
	ListMedia(ctx context.Context) ([]*model.MediaItem, error)
	// FilterMedia — серверная фильтрация: текстовый поиск + AND-теги + коллекция.
	FilterMedia(ctx context.Context, f model.MediaFilter) ([]*model.MediaItem, error)
	// SearchMedia ищет по имени файла, URL, домену и тегам (LIKE).
	SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error)
	// LastMedia возвращает последние n успешно скачанных доступных файлов.
	LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error)
	ClaimNext(ctx context.Context) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) error
	// Cancel переводит активное (checking/pending/retrying) задание в cancelled (мягкая отмена, URL сохраняется).
	Cancel(ctx context.Context, id string) error
	// ConfirmSingle переводит checking-задание в pending (URL оказался одиночным видео).
	ConfirmSingle(ctx context.Context, id string) error
	// DeleteChecking удаляет checking-задание (URL оказался плейлистом, создаём отдельные jobs).
	DeleteChecking(ctx context.Context, id string) error
	// Hide убирает запись из основного интерфейса (архив), данные сохраняются.
	Hide(ctx context.Context, id string) error
	// Unhide возвращает скрытую запись на главную.
	Unhide(ctx context.Context, id string) error
	// Purge безвозвратно удаляет job и все его файлы из БД (только для hidden jobs).
	Purge(ctx context.Context, id string) error
	// CleanupDead удаляет все failed и hidden задания вместе с файловыми записями из БД.
	// Возвращает количество удалённых заданий.
	CleanupDead(ctx context.Context) (int, error)
	ResetFailed(ctx context.Context, id string) error
	ResetStale(ctx context.Context) error
	// Redownload сбрасывает задание в pending и очищает привязку к файлу.
	Redownload(ctx context.Context, id string) error
	// SetTgMessageID сохраняет ID Telegram-сообщения очереди для редактирования/удаления.
	SetTgMessageID(ctx context.Context, jobID string, msgID int64) error
	// SaveLog сохраняет вывод stderr последней попытки скачивания.
	SaveLog(ctx context.Context, jobID, log string) error
	// GetLog возвращает сохранённый лог; пустая строка — лог отсутствует.
	GetLog(ctx context.Context, jobID string) (string, error)
}

type FileRepo interface {
	Create(ctx context.Context, f *model.File) error
	GetByID(ctx context.Context, id string) (*model.File, error)
	ListAll(ctx context.Context) ([]*model.File, error) // включая удалённые/потерянные
	ListByJobID(ctx context.Context, jobID string) ([]*model.File, error)
	// DeleteAllByJobID полностью удаляет все файлы задания из БД (используется при redownload).
	DeleteAllByJobID(ctx context.Context, jobID string) error
	ListDeleted(ctx context.Context) ([]*model.DeletedFile, error)
	// AllPaths возвращает множество всех известных путей (включая удалённые) для сверки при сканировании.
	AllPaths(ctx context.Context) (map[string]struct{}, error)
	// PathsForCleanup возвращает пути файлов, принадлежащих failed/hidden заданиям (для удаления с диска).
	PathsForCleanup(ctx context.Context) ([]string, error)
	// PruneLost удаляет из БД записи файлов, помеченных как потерянные (lost_at IS NOT NULL).
	PruneLost(ctx context.Context) (int, error)
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
	// ListWithCount возвращает все теги с количеством привязанных заданий (для облака тегов).
	ListWithCount(ctx context.Context) ([]*model.TagWithCount, error)
	AddToJob(ctx context.Context, jobID, tagID string) error
	// BulkAddToJobs добавляет один тег сразу к набору заданий.
	BulkAddToJobs(ctx context.Context, tagID string, jobIDs []string) error
	RemoveFromJob(ctx context.Context, jobID, tagName string) error
}

type SettingsRepo interface {
	// Get возвращает значение настройки; пустая строка если не задана.
	Get(ctx context.Context, key string) (string, error)
	// Set сохраняет значение; пустая строка удаляет запись (сброс к дефолту конфига).
	Set(ctx context.Context, key, value string) error
	// All возвращает все сохранённые настройки.
	All(ctx context.Context) (map[string]string, error)
}

type CookieRepo interface {
	// Upsert сохраняет (или заменяет) куки для домена.
	Upsert(ctx context.Context, domain, content string) error
	// List возвращает все записи, отсортированные по домену.
	List(ctx context.Context) ([]*model.CookieRecord, error)
	// Delete удаляет запись домена.
	Delete(ctx context.Context, domain string) error
	// MergeAll объединяет куки всех доменов в один Netscape-блок.
	MergeAll(ctx context.Context) (string, error)
}
