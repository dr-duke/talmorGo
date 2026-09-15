// Package library содержит доменные операции медиатеки: файлы, теги, коллекции
// и постановку пакетных операций в очередь.
//
// Сервис — единственное место, где живут эти правила. HTTP-обработчики и
// Telegram-бот стали тонкими адаптерами: раньше, например, удаление файла
// (стереть с диска + пометить в БД) и redownload были расписаны прямо в
// обработчиках, а бот повторял часть логики по-своему.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/ops"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// OpsRunner — исполнитель фоновых операций.
type OpsRunner interface {
	Enqueue()
	Cancel(id string) bool
}

type Service struct {
	Jobs        repo.JobRepo
	Items       repo.ItemRepo
	Tags        repo.TagRepo
	Tokens      repo.TokenRepo
	Collections repo.CollectionRepo
	Ops         repo.OperationRepo
	Storage     *storage.Storage
	Settings    *settings.Provider
	Cfg         *config.Config
	Runner      OpsRunner
}

// ErrNotAvailable — элемент удалён или потерян.
var ErrNotAvailable = fmt.Errorf("item not available")

// ErrNoLink — постоянной ссылки на элемент не выдавалось.
var ErrNoLink = fmt.Errorf("постоянная ссылка не выдавалась")

// ErrNothingToDo — в запросе не оказалось ни одного элемента.
var ErrNothingToDo = fmt.Errorf("не выбрано ни одного файла")

// ── Выдача медиатеки ─────────────────────────────────────────────────────────

// Page — страница медиатеки: строки и полное число совпадений.
type Page struct {
	Items  []*model.MediaItem
	Total  int
	Filter model.MediaFilter
}

// Media возвращает страницу медиатеки. Размер страницы берётся из настроек,
// если вызывающий не задал свой Limit.
func (s *Service) Media(ctx context.Context, f model.MediaFilter) (*Page, error) {
	if f.Limit == 0 {
		f.Limit = s.Settings.PageSize(ctx)
	}
	items, err := s.Jobs.FilterMedia(ctx, f)
	if err != nil {
		return nil, err
	}

	total := f.Offset + len(items)
	// Полный счётчик нужен, только если страница заполнена целиком:
	// иначе выдача и так дошла до конца.
	if f.Limit > 0 && len(items) >= f.Limit {
		if n, err := s.Jobs.CountMedia(ctx, f); err == nil {
			total = n
		}
	}
	return &Page{Items: items, Total: total, Filter: f}, nil
}

// TagCloud возвращает теги с числом заданий для текущего фильтра.
func (s *Service) TagCloud(ctx context.Context, f model.MediaFilter) ([]*model.TagWithCount, error) {
	return s.Tags.ListWithCountFiltered(ctx, f)
}

// PlaylistEntry — элемент серверного плейлиста для «Воспроизвести всё».
//
// ID нужен фронтенду, чтобы найти текущую позицию в очереди. Раньше он искал
// её сравнением адресов потока, но здесь адрес абсолютный (с BASE_PATH), а в
// разметке строки — относительный, поэтому совпадение не находилось никогда и
// очередь всегда съезжала на второй элемент библиотеки.
type PlaylistEntry struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Title  string `json:"title"`
}

// Playlist собирает все доступные видео фильтра, без ограничения страницей.
func (s *Service) Playlist(ctx context.Context, f model.MediaFilter, basePath string) ([]PlaylistEntry, error) {
	f.Kind = "video"
	f.Limit, f.Offset = 0, 0

	items, err := s.Jobs.FilterMedia(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistEntry, 0, len(items))
	for _, mi := range items {
		if mi.Item == nil || !mi.Item.IsAvailable() {
			continue
		}
		out = append(out, PlaylistEntry{
			ID:     mi.Item.ID,
			Stream: basePath + "/items/" + mi.Item.ID + "/stream",
			Title:  mi.DisplayTitle(),
		})
	}
	return out, nil
}

func (s *Service) ListDeleted(ctx context.Context) ([]*model.DeletedItem, error) {
	return s.Items.ListDeleted(ctx)
}

func (s *Service) Log(ctx context.Context, jobID string) (string, error) {
	return s.Jobs.GetLog(ctx, jobID)
}

// ── Элементы ─────────────────────────────────────────────────────────────────

func (s *Service) Item(ctx context.Context, id string) (*model.Item, error) {
	return s.Items.GetByID(ctx, id)
}

// AvailableItem возвращает элемент, только если его файл на месте.
func (s *Service) AvailableItem(ctx context.Context, id string) (*model.Item, error) {
	item, err := s.Items.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !item.IsAvailable() {
		return nil, ErrNotAvailable
	}
	return item, nil
}

// DeleteItem — мягкое удаление: файл стирается с диска, запись остаётся,
// чтобы сохранить исходную ссылку и имя.
func (s *Service) DeleteItem(ctx context.Context, id string) error {
	item, err := s.Items.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Storage.Delete(item.Path); err != nil {
		return err
	}
	return s.Items.SoftDelete(ctx, id)
}

// RenameItem переименовывает файл на диске и обновляет запись.
func (s *Service) RenameItem(ctx context.Context, id, newName string) error {
	item, err := s.Items.GetByID(ctx, id)
	if err != nil {
		return err
	}
	newPath, err := s.Storage.Rename(item.Path, newName)
	if err != nil {
		return err
	}
	if err := s.Items.Rename(ctx, id, newName, newPath); err != nil {
		// Возвращаем файл на место: иначе запись указывает на старый путь, где
		// файла уже нет, и элемент вскоре помечается потерянным — при том что
		// пользователю показана ошибка «переименование не удалось».
		if rbErr := os.Rename(newPath, item.Path); rbErr != nil {
			slog.Error("library: откат переименования не удался",
				"from", newPath, "to", item.Path, "err", rbErr)
		}
		return err
	}
	return nil
}

// CreateLink возвращает постоянную ссылку на элемент. Базой служит LinkBase,
// поэтому ссылка корректна и когда приложение смонтировано не в корне.
func (s *Service) CreateLink(ctx context.Context, itemID string) (string, error) {
	tok, err := s.Tokens.Upsert(ctx, itemID)
	if err != nil {
		return "", err
	}
	return s.Cfg.LinkBase() + "/f/" + tok.Token, nil
}

// RevokeLink отзывает постоянную ссылку: прежний адрес перестаёт работать.
// Следующий запрос ссылки на этот элемент выдаст новый токен.
func (s *Service) RevokeLink(ctx context.Context, itemID string) error {
	revoked, err := s.Tokens.DeleteByItemID(ctx, itemID)
	if err != nil {
		return err
	}
	if !revoked {
		return ErrNoLink
	}
	return nil
}

// ResolveToken отдаёт элемент по постоянной ссылке.
func (s *Service) ResolveToken(ctx context.Context, token string) (*model.Item, error) {
	t, err := s.Tokens.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.AvailableItem(ctx, t.ItemID)
}

// ── Задания ──────────────────────────────────────────────────────────────────

func (s *Service) HideJob(ctx context.Context, jobID string) error {
	return s.Jobs.Hide(ctx, jobID)
}

func (s *Service) UnhideJob(ctx context.Context, jobID string) error {
	return s.Jobs.Unhide(ctx, jobID)
}

// PurgeJob безвозвратно удаляет скрытое задание вместе с файлами.
// Проверка «скрыто» идёт до удаления файлов: иначе отказ репозитория оставил бы
// пользователя без файлов и с сохранившимся заданием.
func (s *Service) PurgeJob(ctx context.Context, jobID string) error {
	job, err := s.Jobs.GetByID(ctx, jobID)
	if err != nil {
		return err
	}
	if !job.Hidden {
		return fmt.Errorf("задание не скрыто — удаление навсегда недоступно")
	}
	items, err := s.Items.ListByJobID(ctx, jobID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.IsAvailable() {
			if err := s.Storage.Delete(item.Path); err != nil {
				return err
			}
		}
	}
	return s.Jobs.Purge(ctx, jobID)
}

func (s *Service) AddTag(ctx context.Context, jobID, name string) error {
	tag, err := s.Tags.Upsert(ctx, name)
	if err != nil {
		return err
	}
	return s.Tags.AddToJob(ctx, jobID, tag.ID)
}

func (s *Service) RemoveTag(ctx context.Context, jobID, tagName string) error {
	return s.Tags.RemoveFromJob(ctx, jobID, tagName)
}

// ── Коллекции ────────────────────────────────────────────────────────────────

func (s *Service) ListCollections(ctx context.Context) ([]*model.Collection, error) {
	return s.Collections.List(ctx)
}

func (s *Service) CreateCollection(ctx context.Context, name string) (*model.Collection, error) {
	return s.Collections.Create(ctx, name)
}

func (s *Service) RenameCollection(ctx context.Context, id, name string) error {
	return s.Collections.Rename(ctx, id, name)
}

func (s *Service) DeleteCollection(ctx context.Context, id string) error {
	return s.Collections.Delete(ctx, id)
}

func (s *Service) AddJobsToCollection(ctx context.Context, id string, jobIDs []string) error {
	return s.Collections.AddJobs(ctx, id, jobIDs)
}

// ── Фоновые операции ─────────────────────────────────────────────────────────

func (s *Service) Operations(ctx context.Context) ([]*model.Operation, error) {
	return s.Ops.List(ctx, ops.VisibleKinds())
}

func (s *Service) DismissOperation(ctx context.Context, id string) error {
	return s.Ops.Delete(ctx, id)
}

// CancelOperation прерывает выполняющуюся операцию.
func (s *Service) CancelOperation(ctx context.Context, id string) error {
	if s.Runner != nil && s.Runner.Cancel(id) {
		return nil
	}
	// Операция ещё не начата — снимаем её из очереди.
	op, err := s.Ops.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if op.Status != model.OpPending {
		return fmt.Errorf("операция уже завершена")
	}
	return s.Ops.Delete(ctx, id)
}

// enqueue создаёт операцию и будит исполнителя.
func (s *Service) enqueue(ctx context.Context, kind, title string, payload any) error {
	blob, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	op := &model.Operation{Kind: kind, Title: title, Payload: string(blob)}
	if err := s.Ops.Create(ctx, op); err != nil {
		return err
	}
	if s.Runner != nil {
		s.Runner.Enqueue()
	}
	return nil
}

func (s *Service) EnqueueBulkTag(ctx context.Context, tag string, jobIDs []string) error {
	return s.enqueue(ctx, ops.KindBulkTag,
		fmt.Sprintf("Тег «%s» → %d заданий", tag, len(jobIDs)),
		map[string]any{"tag": tag, "job_ids": jobIDs})
}

func (s *Service) EnqueueBulkHide(ctx context.Context, jobIDs []string) error {
	return s.enqueue(ctx, ops.KindBulkHide,
		fmt.Sprintf("Скрыть %d заданий", len(jobIDs)),
		map[string]any{"job_ids": jobIDs})
}

func (s *Service) EnqueueBulkMeta(ctx context.Context, itemIDs []string, fields map[string]string) error {
	return s.enqueue(ctx, ops.KindBulkMeta,
		fmt.Sprintf("Теги аудио → %d файлов", len(itemIDs)),
		map[string]any{"item_ids": itemIDs, "fields": fields})
}

// EnqueueUpdateMeta обновляет теги одного файла. Пустые поля исполнитель
// пропускает — они означают «не трогать», а не «очистить».
func (s *Service) EnqueueUpdateMeta(ctx context.Context, itemID string, meta model.AudioMeta) error {
	fields := map[string]string{
		"title":  meta.Title,
		"artist": meta.Artist,
		"album":  meta.Album,
		"year":   meta.Year,
		"genre":  meta.Genre,
	}
	return s.enqueue(ctx, ops.KindUpdateMeta, "Теги аудио → 1 файл",
		map[string]any{"item_id": itemID, "fields": fields})
}

// EnqueueExtractAudio ставит извлечение дорожек из набора файлов.
// Недоступные файлы отсеиваются сразу, чтобы операция не создавалась впустую.
func (s *Service) EnqueueExtractAudio(ctx context.Context, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return ErrNothingToDo
	}

	var ready []string
	var names []string
	seen := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		// Повтор в запросе не должен превращаться в лишний запуск ffmpeg.
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		item, err := s.AvailableItem(ctx, id)
		if err != nil {
			continue
		}
		ready = append(ready, id)
		names = append(names, item.Name)
	}
	if len(ready) == 0 {
		return ErrNotAvailable
	}

	title := "Извлечь аудио: " + names[0]
	if len(ready) > 1 {
		title = fmt.Sprintf("Извлечь аудио → %d файлов", len(ready))
	}
	return s.enqueue(ctx, ops.KindExtractAudio, title,
		map[string]any{"item_ids": ready})
}

func (s *Service) EnqueueReindex(ctx context.Context) error {
	return s.enqueue(ctx, ops.KindReindex, "Пересчёт тегов и коллекций", struct{}{})
}

func (s *Service) EnqueueCleanup(ctx context.Context) error {
	return s.enqueue(ctx, ops.KindCleanup, "Очистка библиотеки", struct{}{})
}
