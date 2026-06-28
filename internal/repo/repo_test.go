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

func TestFileRepo_CRUD(t *testing.T) {
	database := openTestDB(t)
	r := repo.NewFileRepo(database)
	ctx := context.Background()

	f := &model.File{Path: "/data/video.mp4", Name: "video.mp4", Size: 1024}
	if err := r.Create(ctx, f); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := r.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	if err := r.Rename(ctx, f.ID, "renamed.mp4", "/data/renamed.mp4"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ := r.GetByID(ctx, f.ID)
	if got.Name != "renamed.mp4" {
		t.Errorf("name after rename: %s", got.Name)
	}

	if err := r.Delete(ctx, f.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// После soft delete запись остаётся, но помечена удалённой.
	got, _ = r.GetByID(ctx, f.ID)
	if got.IsAvailable() {
		t.Errorf("expected file to be unavailable after soft delete")
	}
}

func TestTokenRepo_Upsert(t *testing.T) {
	database := openTestDB(t)
	fileRepo := repo.NewFileRepo(database)
	tokenRepo := repo.NewTokenRepo(database)
	ctx := context.Background()

	f := &model.File{Path: "/data/v.mp4", Name: "v.mp4", Size: 512}
	if err := fileRepo.Create(ctx, f); err != nil {
		t.Fatalf("create file: %v", err)
	}

	t1, err := tokenRepo.Upsert(ctx, f.ID)
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	t2, err := tokenRepo.Upsert(ctx, f.ID)
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
	if got.FileID != f.ID {
		t.Errorf("file_id mismatch: %s vs %s", got.FileID, f.ID)
	}
}
