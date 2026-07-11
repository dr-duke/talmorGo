package ops

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/dr-duke/talmorGo/internal/audio"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
)

// Worker исполняет пакетные операции в фоне, по одной за раз.
type Worker struct {
	Ops   repo.OperationRepo
	Tags  repo.TagRepo
	Jobs  repo.JobRepo
	Items repo.ItemRepo
	Cfg   *config.Config
	Hub   *sse.Hub
	ch    chan struct{}
}

func NewWorker(
	ops repo.OperationRepo,
	tags repo.TagRepo,
	jobs repo.JobRepo,
	items repo.ItemRepo,
	cfg *config.Config,
	hub *sse.Hub,
) *Worker {
	return &Worker{
		Ops: ops, Tags: tags, Jobs: jobs, Items: items, Cfg: cfg, Hub: hub,
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
