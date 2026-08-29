package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dr-duke/talmorGo/internal/audio"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/filecheck"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// ErrCancelled — операция прервана пользователем.
var ErrCancelled = errors.New("операция отменена")

// Worker исполняет пакетные операции в фоне. Полосы (light/heavy) работают
// параллельно и независимо, внутри полосы операции идут по одной.
//
// Каждая операция получает собственный отменяемый контекст с таймаутом:
// зависший ffmpeg больше не блокирует очередь навсегда, а пользователь может
// прервать операцию из интерфейса.
type Worker struct {
	Ops     repo.OperationRepo
	Tags    repo.TagRepo
	Jobs    repo.JobRepo
	Items   repo.ItemRepo
	Storage *storage.Storage
	Cfg     *config.Config
	Hub     *sse.Hub

	lanes []*lane

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

type lane struct {
	class string
	kinds []string
	ch    chan struct{}
}

func NewWorker(
	ops repo.OperationRepo,
	tags repo.TagRepo,
	jobs repo.JobRepo,
	items repo.ItemRepo,
	store *storage.Storage,
	cfg *config.Config,
	hub *sse.Hub,
) *Worker {
	return &Worker{
		Ops: ops, Tags: tags, Jobs: jobs, Items: items, Storage: store, Cfg: cfg, Hub: hub,
		lanes: []*lane{
			{class: ClassLight, kinds: KindsOfClass(ClassLight), ch: make(chan struct{}, 1)},
			{class: ClassHeavy, kinds: KindsOfClass(ClassHeavy), ch: make(chan struct{}, 1)},
		},
		cancels: make(map[string]context.CancelFunc),
	}
}

// Enqueue сигнализирует всем полосам о появлении новой операции (non-blocking).
func (w *Worker) Enqueue() {
	for _, l := range w.lanes {
		select {
		case l.ch <- struct{}{}:
		default:
		}
	}
}

// Cancel прерывает выполняющуюся операцию. Возвращает true, если операция была запущена.
func (w *Worker) Cancel(id string) bool {
	w.mu.Lock()
	cancel, ok := w.cancels[id]
	w.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Start восстанавливает оборванные операции и запускает полосы; блокируется до отмены ctx.
func (w *Worker) Start(ctx context.Context) {
	if n, err := w.Ops.ResetStale(ctx); err != nil {
		slog.Error("ops: reset stale operations", "err", err)
	} else if n > 0 {
		slog.Info("ops: stale operations returned to queue", "count", n)
	}

	var wg sync.WaitGroup
	for _, l := range w.lanes {
		wg.Add(1)
		go func(l *lane) {
			defer wg.Done()
			w.runLane(ctx, l)
		}(l)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runRetention(ctx)
	}()

	w.Enqueue() // подхватываем то, что осталось в очереди с прошлого запуска
	wg.Wait()
}

// runRetention периодически подчищает завершённые операции.
// Видимые записи пользователь закрывает вручную и делает это редко, поэтому без
// уборки таблица растёт бесконечно.
func (w *Worker) runRetention(ctx context.Context) {
	retention := w.retention()
	if retention <= 0 {
		return
	}
	w.pruneOperations(ctx, retention)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pruneOperations(ctx, retention)
		}
	}
}

func (w *Worker) pruneOperations(ctx context.Context, retention time.Duration) {
	n, err := w.Ops.DeleteFinishedBefore(ctx, time.Now().Add(-retention))
	if err != nil {
		slog.Error("ops: prune finished operations", "err", err)
		return
	}
	if n > 0 {
		slog.Info("ops: finished operations pruned", "count", n, "older_than", retention)
		w.publish(sse.TopicQueue)
	}
}

func (w *Worker) retention() time.Duration {
	if w.Cfg == nil {
		return 0
	}
	return time.Duration(w.Cfg.OpsRetentionHours) * time.Hour
}

func (w *Worker) runLane(ctx context.Context, l *lane) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.ch:
			w.drainPending(ctx, l)
		}
	}
}

func (w *Worker) drainPending(ctx context.Context, l *lane) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("ops: panic in lane", "class", l.class, "panic", r)
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		op, err := w.Ops.ClaimNext(ctx, l.kinds)
		if err != nil {
			slog.Error("ops: claim next", "class", l.class, "err", err)
			return
		}
		if op == nil {
			return
		}
		w.runOne(ctx, op)
	}
}

// runOne исполняет одну операцию под собственным контекстом с таймаутом.
func (w *Worker) runOne(ctx context.Context, op *model.Operation) {
	opCtx, cancel := context.WithTimeout(ctx, w.opTimeout())
	w.mu.Lock()
	w.cancels[op.ID] = cancel
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.cancels, op.ID)
		w.mu.Unlock()
		cancel()
	}()

	w.publish(sse.TopicQueue) // уведомить UI: статус running

	execErr := w.exec(opCtx, op)

	// Прерывание пользователем и таймаут отражаем понятным текстом.
	if execErr != nil && opCtx.Err() != nil {
		if errors.Is(opCtx.Err(), context.DeadlineExceeded) {
			execErr = fmt.Errorf("превышен таймаут %s", w.opTimeout())
		} else {
			execErr = ErrCancelled
		}
	}

	if execErr != nil {
		slog.Error("ops: exec failed", "kind", op.Kind, "id", op.ID, "err", execErr)
		if err := w.Ops.SetFailed(context.WithoutCancel(ctx), op.ID, execErr.Error()); err != nil {
			slog.Error("ops: set failed", "err", err)
		}
	} else if err := w.Ops.SetDone(context.WithoutCancel(ctx), op.ID); err != nil {
		slog.Error("ops: set done", "err", err)
	}
	w.publish(topicsFor(op.Kind)...) // уведомить UI: операция завершена

	// Невидимые операции удаляем сразу — они не нужны в очереди и не имеют кнопки dismiss.
	if !ShowInQueue[op.Kind] {
		if err := w.Ops.Delete(context.WithoutCancel(ctx), op.ID); err != nil {
			slog.Warn("ops: delete hidden op record", "id", op.ID, "err", err)
		}
	}
}

func (w *Worker) exec(ctx context.Context, op *model.Operation) error {
	switch op.Kind {
	case KindBulkTag:
		return w.execBulkTag(ctx, op)
	case KindBulkHide:
		return w.execBulkHide(ctx, op)
	case KindBulkMeta:
		return w.execBulkMeta(ctx, op)
	case KindExtractAudio:
		return w.execExtractAudio(ctx, op)
	case KindUpdateMeta:
		return w.execUpdateMeta(ctx, op)
	case KindReindex:
		return w.execReindex(ctx, op)
	case KindCleanup:
		return w.execCleanup(ctx, op)
	default:
		slog.Warn("ops: unknown kind", "kind", op.Kind)
		return nil
	}
}

func (w *Worker) opTimeout() time.Duration {
	if w.Cfg != nil && w.Cfg.OpsTimeout > 0 {
		return time.Duration(w.Cfg.OpsTimeout) * time.Second
	}
	return time.Hour
}

func (w *Worker) publish(topics ...sse.Topic) {
	if w.Hub != nil {
		w.Hub.Publish(topics...)
	}
}

// topicsFor возвращает области интерфейса, которые меняет операция данного вида.
// Сама запись операции всегда видна в очереди, поэтому TopicQueue есть везде.
func topicsFor(kind string) []sse.Topic {
	switch kind {
	case KindBulkTag, KindBulkHide:
		return []sse.Topic{sse.TopicQueue, sse.TopicLibrary, sse.TopicTags}
	case KindBulkMeta, KindUpdateMeta, KindExtractAudio:
		return []sse.Topic{sse.TopicQueue, sse.TopicLibrary}
	case KindReindex:
		return []sse.Topic{sse.TopicQueue, sse.TopicLibrary, sse.TopicTags, sse.TopicCollections}
	case KindCleanup:
		return []sse.Topic{sse.TopicQueue, sse.TopicLibrary, sse.TopicTags, sse.TopicCollections}
	default:
		return []sse.Topic{sse.TopicQueue, sse.TopicLibrary}
	}
}

// ── payload structs ──────────────────────────────────────────────────────────

type bulkTagPayload struct {
	TagName string   `json:"tag"`
	JobIDs  []string `json:"job_ids"`
}

type bulkHidePayload struct {
	JobIDs []string `json:"job_ids"`
}

type bulkMetaPayload struct {
	ItemIDs []string          `json:"item_ids"`
	Fields  map[string]string `json:"fields"`
}

func (w *Worker) execBulkTag(ctx context.Context, op *model.Operation) error {
	var p bulkTagPayload
	if err := json.Unmarshal([]byte(op.Payload), &p); err != nil {
		return err
	}
	tag, err := w.Tags.Upsert(ctx, p.TagName)
	if err != nil {
		return err
	}
	return w.Tags.BulkAddToJobs(ctx, tag.ID, p.JobIDs)
}

func (w *Worker) execBulkHide(ctx context.Context, op *model.Operation) error {
	var p bulkHidePayload
	if err := json.Unmarshal([]byte(op.Payload), &p); err != nil {
		return err
	}
	for _, id := range p.JobIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.Jobs.Hide(ctx, id); err != nil {
			slog.Warn("ops: hide job", "id", id, "err", err)
		}
	}
	return nil
}

func (w *Worker) execBulkMeta(ctx context.Context, op *model.Operation) error {
	var p bulkMetaPayload
	if err := json.Unmarshal([]byte(op.Payload), &p); err != nil {
		return err
	}
	if err := w.Items.BulkUpdateMetaFields(ctx, p.ItemIDs, p.Fields); err != nil {
		return err
	}
	for _, id := range p.ItemIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		item, err := w.Items.GetByID(ctx, id)
		if err != nil || item.IsDeleted() || item.IsLost() || item.Kind != "audio" {
			continue
		}
		if err := audio.WriteTags(ctx, w.Cfg.FfmpegBinary, item.Path, p.Fields); err != nil {
			slog.Warn("ops: write tags", "item_id", id, "err", err)
		}
	}
	return nil
}

// ── ExtractAudio ─────────────────────────────────────────────────────────────

type extractAudioPayload struct {
	ItemID string `json:"item_id"`
}

func (w *Worker) execExtractAudio(ctx context.Context, op *model.Operation) error {
	var p extractAudioPayload
	if err := json.Unmarshal([]byte(op.Payload), &p); err != nil {
		return err
	}
	src, err := w.Items.GetByID(ctx, p.ItemID)
	if err != nil {
		return fmt.Errorf("get item: %w", err)
	}
	if !src.IsAvailable() {
		return fmt.Errorf("item not available")
	}

	var meta model.AudioMeta
	if job, err := w.Jobs.GetByID(ctx, src.JobID); err == nil {
		meta.Title = job.Title
		meta.Artist = job.Domain()
	}

	outPath, err := audio.Extract(ctx, w.Cfg.FfmpegBinary, src.Path, w.Cfg.AudioDir(), meta)
	if err != nil {
		return fmt.Errorf("ffmpeg extract: %w", err)
	}

	var size int64
	if info, err := os.Stat(outPath); err == nil {
		size = info.Size()
	}

	audioItem := &model.Item{
		JobID: src.JobID,
		Kind:  "audio",
		Path:  outPath,
		Name:  filepath.Base(outPath),
		Size:  size,
		Meta:  meta,
	}
	if err := w.Items.Create(ctx, audioItem); err != nil {
		return fmt.Errorf("save audio item: %w", err)
	}
	slog.Info("ops: audio extracted", "src", src.Path, "dst", outPath)
	return nil
}

// ── UpdateMeta ───────────────────────────────────────────────────────────────

type updateMetaPayload struct {
	ItemID string            `json:"item_id"`
	Fields map[string]string `json:"fields"`
}

func (w *Worker) execUpdateMeta(ctx context.Context, op *model.Operation) error {
	var p updateMetaPayload
	if err := json.Unmarshal([]byte(op.Payload), &p); err != nil {
		return err
	}
	// Пропускаем пустые значения — пустое поле означает «не трогать», а не «очистить».
	fields := make(map[string]string, len(p.Fields))
	for k, v := range p.Fields {
		if v != "" {
			fields[k] = v
		}
	}
	if len(fields) == 0 {
		return nil
	}
	if err := w.Items.BulkUpdateMetaFields(ctx, []string{p.ItemID}, fields); err != nil {
		return err
	}
	item, err := w.Items.GetByID(ctx, p.ItemID)
	if err != nil || item.IsDeleted() || item.IsLost() || item.Kind != "audio" {
		return nil //nolint:nilerr // элемент недоступен — записывать теги в файл нечего
	}
	return audio.WriteTags(ctx, w.Cfg.FfmpegBinary, item.Path, fields)
}

// ── Reindex ──────────────────────────────────────────────────────────────────

func (w *Worker) execReindex(ctx context.Context, op *model.Operation) error {
	nJobTags, nTags, nCollections, err := w.Tags.PruneOrphans(ctx)
	if err != nil {
		slog.Error("ops: reindex prune orphans", "err", err)
	}
	lost, found, err := filecheck.Run(ctx, w.Items)
	if err != nil {
		return fmt.Errorf("check files: %w", err)
	}
	slog.Info("ops: reindex done",
		"job_tags_pruned", nJobTags, "tags_pruned", nTags, "collections_pruned", nCollections,
		"files_lost", lost, "files_found", found)
	return nil
}

// ── Cleanup ──────────────────────────────────────────────────────────────────

func (w *Worker) execCleanup(ctx context.Context, op *model.Operation) error {
	paths, err := w.Items.PathsForCleanup(ctx)
	if err != nil {
		slog.Error("ops: cleanup paths", "err", err)
	}
	for _, p := range paths {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if delErr := w.Storage.Delete(p); delErr != nil {
			slog.Warn("ops: cleanup delete file", "path", p, "err", delErr)
		}
	}
	nJobs, err := w.Jobs.CleanupDead(ctx)
	if err != nil {
		slog.Error("ops: cleanup dead jobs", "err", err)
	}
	nFiles, err := w.Items.PruneLost(ctx)
	if err != nil {
		slog.Error("ops: prune lost", "err", err)
	}
	slog.Info("ops: cleanup done",
		"files_deleted", len(paths), "jobs_deleted", nJobs, "lost_pruned", nFiles)
	return nil
}
