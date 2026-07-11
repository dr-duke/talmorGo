package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dr-duke/talmorGo/internal/audio"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/internal/storage"
)

// Worker исполняет пакетные операции в фоне, по одной за раз.
type Worker struct {
	Ops     repo.OperationRepo
	Tags    repo.TagRepo
	Jobs    repo.JobRepo
	Items   repo.ItemRepo
	Storage *storage.Storage
	Cfg     *config.Config
	Hub     *sse.Hub
	ch      chan struct{}
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
		ch: make(chan struct{}, 1),
	}
}

// Enqueue сигнализирует воркеру о наличии новой pending-операции (non-blocking).
func (w *Worker) Enqueue() {
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// Start запускает цикл обработки; блокируется до отмены ctx.
func (w *Worker) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.ch:
			w.drainPending(ctx)
		}
	}
}

func (w *Worker) drainPending(ctx context.Context) {
	for {
		op, err := w.Ops.ClaimNext(ctx)
		if err != nil {
			slog.Error("ops: claim next", "err", err)
			return
		}
		if op == nil {
			return
		}
		w.Hub.Broadcast() // уведомить UI: статус running

		var execErr error
		switch op.Kind {
		case KindBulkTag:
			execErr = w.execBulkTag(ctx, op)
		case KindBulkHide:
			execErr = w.execBulkHide(ctx, op)
		case KindBulkMeta:
			execErr = w.execBulkMeta(ctx, op)
		case KindExtractAudio:
			execErr = w.execExtractAudio(ctx, op)
		case KindUpdateMeta:
			execErr = w.execUpdateMeta(ctx, op)
		case KindReindex:
			execErr = w.execReindex(ctx, op)
		case KindCleanup:
			execErr = w.execCleanup(ctx, op)
		default:
			slog.Warn("ops: unknown kind", "kind", op.Kind)
		}

		if execErr != nil {
			slog.Error("ops: exec failed", "kind", op.Kind, "id", op.ID, "err", execErr)
			if err := w.Ops.SetFailed(ctx, op.ID, execErr.Error()); err != nil {
				slog.Error("ops: set failed", "err", err)
			}
		} else {
			if err := w.Ops.SetDone(ctx, op.ID); err != nil {
				slog.Error("ops: set done", "err", err)
			}
		}
		w.Hub.Broadcast() // уведомить UI: операция завершена
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
	if err := w.Items.BulkUpdateMetaFields(ctx, []string{p.ItemID}, p.Fields); err != nil {
		return err
	}
	item, err := w.Items.GetByID(ctx, p.ItemID)
	if err != nil || item.IsDeleted() || item.IsLost() || item.Kind != "audio" {
		return nil
	}
	return audio.WriteTags(ctx, w.Cfg.FfmpegBinary, item.Path, p.Fields)
}

// ── Reindex ──────────────────────────────────────────────────────────────────

func (w *Worker) execReindex(ctx context.Context, op *model.Operation) error {
	nJobTags, nTags, nCollections, err := w.Tags.PruneOrphans(ctx)
	if err != nil {
		slog.Error("ops: reindex prune orphans", "err", err)
	}
	items, err := w.Items.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list items: %w", err)
	}
	lost, found := 0, 0
	for _, item := range items {
		if item.IsDeleted() {
			continue
		}
		_, statErr := os.Stat(item.Path)
		missing := os.IsNotExist(statErr)
		if missing && !item.IsLost() {
			if e := w.Items.MarkLost(ctx, item.ID); e == nil {
				lost++
			}
		} else if !missing && item.IsLost() {
			if e := w.Items.MarkFound(ctx, item.ID); e == nil {
				found++
			}
		}
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
