package repo_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestJobRepo_CreateAndGet(t *testing.T) {
	database := openTestDB(t)
	r := repo.NewJobRepo(database)
	ctx := context.Background()

	job := &model.Job{
		URL:    "https://example.com/video",
		Status: model.JobPending,
		Source: "web",
	}
	if err := r.Create(ctx, job); err != nil {
		t.Fatalf("create: %v", err)
	}
	if job.ID == "" {
		t.Fatal("id not assigned")
	}

	got, err := r.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != job.URL {
		t.Errorf("url: got %s, want %s", got.URL, job.URL)
	}
	if got.Status != model.JobPending {
		t.Errorf("status: got %s, want pending", got.Status)
	}
}

func TestJobRepo_ClaimNext(t *testing.T) {
	database := openTestDB(t)
	r := repo.NewJobRepo(database)
	ctx := context.Background()

	// Нет задач — ClaimNext должен вернуть nil, nil.
	j, err := r.ClaimNext(ctx)
	if err != nil || j != nil {
		t.Fatalf("expected nil job, got %v %v", j, err)
	}

	job := &model.Job{URL: "https://example.com/1", Status: model.JobPending, Source: "web"}
	if err := r.Create(ctx, job); err != nil {
		t.Fatalf("create: %v", err)
	}

	claimed, err := r.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected claimed job")
	}
	if claimed.Status != model.JobRunning {
		t.Errorf("status after claim: got %s, want running", claimed.Status)
	}

	// Повторный ClaimNext — очередь пуста.
	j2, err := r.ClaimNext(ctx)
	if err != nil || j2 != nil {
		t.Fatalf("expected nil after queue empty, got %v %v", j2, err)
	}
}

func TestJobRepo_ResetStale(t *testing.T) {
	database := openTestDB(t)
	r := repo.NewJobRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/2", Status: model.JobRunning, Source: "web"}
	if err := r.Create(ctx, job); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := r.ResetStale(ctx); err != nil {
		t.Fatalf("reset stale: %v", err)
	}

	got, err := r.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.JobPending {
		t.Errorf("status after reset: got %s, want pending", got.Status)
	}
}

func TestItemRepo_CRUD(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	r := repo.NewItemRepo(database)
	ctx := context.Background()

	// Item требует существующего job для FK.
	job := &model.Job{URL: "local", Status: model.JobImported, Source: "filesystem"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	item := &model.Item{JobID: job.ID, Kind: "video", Path: "/data/video.mp4", Name: "video.mp4", Size: 1024}
	if err := r.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := r.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	if err := r.Rename(ctx, item.ID, "renamed.mp4", "/data/renamed.mp4"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ := r.GetByID(ctx, item.ID)
	if got.Name != "renamed.mp4" {
		t.Errorf("name after rename: %s", got.Name)
	}

	if err := r.SoftDelete(ctx, item.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// После soft delete запись остаётся, но помечена удалённой.
	got, _ = r.GetByID(ctx, item.ID)
	if got.IsAvailable() {
		t.Errorf("expected item to be unavailable after soft delete")
	}
}

func TestTokenRepo_Upsert(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	itemRepo := repo.NewItemRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "local", Status: model.JobImported, Source: "filesystem"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	item := &model.Item{JobID: job.ID, Kind: "video", Path: "/data/v.mp4", Name: "v.mp4", Size: 512}
	if err := itemRepo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}

	t1, err := tokenRepo.Upsert(ctx, item.ID)
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	t2, err := tokenRepo.Upsert(ctx, item.ID)
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if t1.Token != t2.Token {
		t.Errorf("upsert should return same token: %s vs %s", t1.Token, t2.Token)
	}

	got, err := tokenRepo.GetByToken(ctx, t1.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if got.ItemID != item.ID {
		t.Errorf("item_id mismatch: %s vs %s", got.ItemID, item.ID)
	}
}

func TestTagRepo_PruneOnRemove(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	tagRepo := repo.NewTagRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/v", Status: model.JobDone, Source: "web"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	tag, err := tagRepo.Upsert(ctx, "prunable")
	if err != nil {
		t.Fatalf("upsert tag: %v", err)
	}
	if err := tagRepo.AddToJob(ctx, job.ID, tag.ID); err != nil {
		t.Fatalf("add tag: %v", err)
	}

	// Тег должен быть виден в ListWithCount.
	counts, _ := tagRepo.ListWithCount(ctx)
	if len(counts) == 0 {
		t.Fatal("tag should appear before removal")
	}

	// Удаляем тег с job — он становится сиротой.
	if err := tagRepo.RemoveFromJob(ctx, job.ID, "prunable"); err != nil {
		t.Fatalf("remove tag: %v", err)
	}

	// ListWithCount не должен показывать сиротский тег.
	counts, _ = tagRepo.ListWithCount(ctx)
	for _, tw := range counts {
		if tw.Name == "prunable" {
			t.Error("orphan tag still shown in ListWithCount after RemoveFromJob")
		}
	}

	// Тег физически должен быть удалён из таблицы.
	all, _ := tagRepo.ListAll(ctx)
	for _, tg := range all {
		if tg.Name == "prunable" {
			t.Error("orphan tag still present in tags table after RemoveFromJob")
		}
	}
}

func TestTagRepo_PruneOrphans(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	tagRepo := repo.NewTagRepo(database)
	ctx := context.Background()

	// Создаём job и привязываем тег
	job := &model.Job{URL: "https://example.com/v", Status: model.JobDone, Source: "web"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	tag, err := tagRepo.Upsert(ctx, "keep-tag")
	if err != nil {
		t.Fatalf("upsert tag: %v", err)
	}
	if err := tagRepo.AddToJob(ctx, job.ID, tag.ID); err != nil {
		t.Fatalf("add tag: %v", err)
	}

	// Вручную вставляем оборванную job_tags запись (job_id не существует)
	database.ExecContext(ctx, `INSERT INTO job_tags (job_id, tag_id) VALUES ('ghost-job-id', ?)`, tag.ID)

	// Вручную вставляем сиротский тег (kind=plain, нет job_tags)
	database.ExecContext(ctx, `INSERT INTO tags (id, name, kind) VALUES ('orphan-tag-id', 'orphan-tag', 'plain')`)

	nJobTags, nTags, nCollections, err := tagRepo.PruneOrphans(ctx)
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if nJobTags != 1 {
		t.Errorf("expected 1 orphaned job_tag pruned, got %d", nJobTags)
	}
	if nTags != 1 {
		t.Errorf("expected 1 orphaned tag pruned, got %d", nTags)
	}
	if nCollections != 0 {
		t.Errorf("expected 0 collections pruned, got %d", nCollections)
	}

	// keep-tag должен остаться
	all, _ := tagRepo.ListAll(ctx)
	found := false
	for _, tg := range all {
		if tg.Name == "keep-tag" {
			found = true
		}
		if tg.Name == "orphan-tag" {
			t.Error("orphan-tag should have been pruned")
		}
	}
	if !found {
		t.Error("keep-tag should still exist after PruneOrphans")
	}
}
