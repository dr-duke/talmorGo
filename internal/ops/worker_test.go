package ops_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/ops"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/internal/storage"
)

type env struct {
	worker *ops.Worker
	ops    repo.OperationRepo
	jobs   repo.JobRepo
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{YtDlpOutputDir: dir, FfmpegBinary: "ffmpeg", OpsTimeout: 30}
	opRepo := repo.NewOperationRepo(database)
	jobRepo := repo.NewJobRepo(database)

	w := ops.NewWorker(opRepo, repo.NewTagRepo(database), jobRepo,
		repo.NewItemRepo(database), storage.New(dir), cfg, sse.New())

	return &env{worker: w, ops: opRepo, jobs: jobRepo}
}

// run запускает воркер на время теста.
func (e *env) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.worker.Start(ctx)
}

// waitOpsEmpty ждёт, пока в очереди не останется незавершённых операций.
func (e *env) waitDone(t *testing.T, opID string) *model.Operation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := e.ops.GetByID(context.Background(), opID)
		if err == nil && (op.Status == model.OpDone || op.Status == model.OpFailed) {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not finish in time")
	return nil
}

func TestWorker_RunsLightOperation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/a", Status: model.JobDone, Source: "web"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	op := &model.Operation{
		Kind:    ops.KindBulkHide,
		Title:   "Скрыть 1 задание",
		Payload: `{"job_ids":["` + job.ID + `"]}`,
	}
	if err := e.ops.Create(ctx, op); err != nil {
		t.Fatal(err)
	}

	e.run(t)
	e.worker.Enqueue()

	got := e.waitDone(t, op.ID)
	if got.Status != model.OpDone {
		t.Fatalf("status = %s (%s), want done", got.Status, got.Error)
	}

	hidden, _ := e.jobs.GetByID(ctx, job.ID)
	if !hidden.Hidden {
		t.Error("job must be hidden after the operation")
	}
}

// Операция, оборванная рестартом, должна вернуться в очередь, а не висеть
// в running навсегда.
func TestWorker_RecoversStaleOperation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/b", Status: model.JobDone, Source: "web"}
	if err := e.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	op := &model.Operation{
		Kind:    ops.KindBulkHide,
		Title:   "Скрыть 1 задание",
		Payload: `{"job_ids":["` + job.ID + `"]}`,
	}
	if err := e.ops.Create(ctx, op); err != nil {
		t.Fatal(err)
	}
	// Имитируем обрыв: операция осталась в статусе running.
	if _, err := e.ops.ClaimNext(ctx, nil); err != nil {
		t.Fatal(err)
	}

	e.run(t)

	got := e.waitDone(t, op.ID)
	if got.Status != model.OpDone {
		t.Errorf("stale operation status = %s (%s), want done", got.Status, got.Error)
	}
}

// Завершённые операции подчищаются по сроку хранения, незавершённые — нет.
func TestOperations_RetentionPrunesOldFinished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	mk := func(kind string, finish func(id string)) string {
		op := &model.Operation{Kind: kind, Title: "оп", Payload: `{}`}
		if err := e.ops.Create(ctx, op); err != nil {
			t.Fatal(err)
		}
		if finish != nil {
			finish(op.ID)
		}
		return op.ID
	}

	doneID := mk(ops.KindBulkTag, func(id string) {
		if err := e.ops.SetDone(ctx, id); err != nil {
			t.Fatal(err)
		}
	})
	failedID := mk(ops.KindBulkTag, func(id string) {
		if err := e.ops.SetFailed(ctx, id, "ошибка"); err != nil {
			t.Fatal(err)
		}
	})
	pendingID := mk(ops.KindBulkTag, nil)

	// Срок хранения ещё не вышел — ничего не удаляем.
	n, err := e.ops.DeleteFinishedBefore(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("удалено %d свежих операций, ожидалось 0", n)
	}

	// Срок вышел — уходят только завершённые.
	n, err = e.ops.DeleteFinishedBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("удалено %d операций, ожидалось 2", n)
	}
	for _, id := range []string{doneID, failedID} {
		if _, err := e.ops.GetByID(ctx, id); err == nil {
			t.Errorf("завершённая операция %s осталась", id)
		}
	}
	if _, err := e.ops.GetByID(ctx, pendingID); err != nil {
		t.Error("ожидающая операция не должна удаляться по сроку хранения")
	}
}

// Полосы исполнения независимы: лёгкая операция не ждёт тяжёлую.
func TestClasses_PartitionAllKinds(t *testing.T) {
	light := ops.KindsOfClass(ops.ClassLight)
	heavy := ops.KindsOfClass(ops.ClassHeavy)

	if len(light)+len(heavy) != len(ops.Class) {
		t.Errorf("классы покрывают %d видов из %d", len(light)+len(heavy), len(ops.Class))
	}
	for kind := range ops.ShowInQueue {
		if _, ok := ops.Class[kind]; !ok {
			t.Errorf("вид %q не отнесён ни к одной полосе — операция никогда не выполнится", kind)
		}
	}
	// Массовое тегирование обязано идти в лёгкой полосе: иначе оно ждёт ffmpeg.
	if ops.Class[ops.KindBulkTag] != ops.ClassLight {
		t.Error("bulk_tag должен исполняться в лёгкой полосе")
	}
	if ops.Class[ops.KindExtractAudio] != ops.ClassHeavy {
		t.Error("extract_audio должен исполняться в тяжёлой полосе")
	}
}

// ClaimNext обязан уважать границы полосы, иначе воркеры разберут чужие операции.
func TestClaimNext_RespectsKinds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	heavy := &model.Operation{Kind: ops.KindBulkMeta, Title: "Теги", Payload: `{}`}
	if err := e.ops.Create(ctx, heavy); err != nil {
		t.Fatal(err)
	}

	got, err := e.ops.ClaimNext(ctx, ops.KindsOfClass(ops.ClassLight))
	if err != nil {
		t.Fatalf("claim light: %v", err)
	}
	if got != nil {
		t.Fatalf("лёгкая полоса забрала %s", got.Kind)
	}

	got, err = e.ops.ClaimNext(ctx, ops.KindsOfClass(ops.ClassHeavy))
	if err != nil {
		t.Fatalf("claim heavy: %v", err)
	}
	if got == nil || got.Kind != ops.KindBulkMeta {
		t.Fatalf("тяжёлая полоса не забрала операцию: %v", got)
	}
	if got.Status != model.OpRunning {
		t.Errorf("status = %s, want running", got.Status)
	}
}
