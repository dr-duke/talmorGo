package repo_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
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

// ResetStale возвращает в очередь running, но не трогает checking: такое задание
// не прошло проверку на плейлист, и pending скачал бы плейлист одним заданием.
func TestJobRepo_ResetStale_LeavesChecking(t *testing.T) {
	database := openTestDB(t)
	r := repo.NewJobRepo(database)
	ctx := context.Background()

	running := &model.Job{URL: "https://example.com/2", Status: model.JobRunning, Source: "web"}
	if err := r.Create(ctx, running); err != nil {
		t.Fatalf("create: %v", err)
	}
	checking := &model.Job{URL: "https://example.com/list", Status: model.JobChecking, Source: "web"}
	if err := r.Create(ctx, checking); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := r.ResetStale(ctx); err != nil {
		t.Fatalf("reset stale: %v", err)
	}

	got, _ := r.GetByID(ctx, running.ID)
	if got.Status != model.JobPending {
		t.Errorf("running after reset: got %s, want pending", got.Status)
	}
	got, _ = r.GetByID(ctx, checking.ID)
	if got.Status != model.JobChecking {
		t.Errorf("checking after reset: got %s, want checking", got.Status)
	}

	pending, err := r.ListChecking(ctx)
	if err != nil {
		t.Fatalf("list checking: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != checking.ID {
		t.Errorf("ListChecking = %v, want the checking job", pending)
	}
}

// Purge не должен трогать нескрытое задание: иначе его файлы исчезали из БД,
// а само задание оставалось.
func TestJobRepo_PurgeOnlyHidden(t *testing.T) {
	database := openTestDB(t)
	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/v", Status: model.JobDone, Source: "web"}
	if err := jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	item := &model.Item{JobID: job.ID, Kind: "video", Path: "/data/keep.mp4", Name: "keep.mp4"}
	if err := items.Create(ctx, item); err != nil {
		t.Fatal(err)
	}

	if err := jobs.Purge(ctx, job.ID); err == nil {
		t.Error("purge of a visible job must fail")
	}
	list, _ := items.ListByJobID(ctx, job.ID)
	if len(list) != 1 {
		t.Errorf("items after refused purge = %d, want 1", len(list))
	}

	// После скрытия удаление проходит и уносит элементы каскадом.
	if err := jobs.Hide(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Purge(ctx, job.ID); err != nil {
		t.Fatalf("purge hidden: %v", err)
	}
	list, _ = items.ListByJobID(ctx, job.ID)
	if len(list) != 0 {
		t.Errorf("items after purge = %d, want 0", len(list))
	}
}

func TestItemRepo_CRUD(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	r := repo.NewItemRepo(database)
	ctx := context.Background()

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
	got, _ = r.GetByID(ctx, item.ID)
	if got.IsAvailable() {
		t.Errorf("expected item to be unavailable after soft delete")
	}
}

// Файл, вернувшийся на диск по тому же пути, должен снова стать доступным:
// раньше ON CONFLICT сохранял deleted_at и запись оставалась «удалённой».
func TestItemRepo_CreateClearsDeletedAndLost(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "local", Status: model.JobImported, Source: "filesystem"}
	if err := jobRepo.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	item := &model.Item{JobID: job.ID, Kind: "video", Path: "/data/again.mp4", Name: "again.mp4"}
	if err := items.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := items.SoftDelete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}

	// Сканер снова находит файл по тому же пути.
	reimported := &model.Item{JobID: job.ID, Kind: "video", Path: "/data/again.mp4", Name: "again.mp4", Size: 42}
	if err := items.Create(ctx, reimported); err != nil {
		t.Fatalf("re-create: %v", err)
	}

	got, err := items.GetByID(ctx, reimported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAvailable() {
		t.Error("re-imported item must be available again")
	}
	if got.Size != 42 {
		t.Errorf("size = %d, want 42", got.Size)
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

	counts, _ := tagRepo.ListWithCountFiltered(ctx, model.MediaFilter{})
	if len(counts) == 0 {
		t.Fatal("tag should appear before removal")
	}

	if err := tagRepo.RemoveFromJob(ctx, job.ID, "prunable"); err != nil {
		t.Fatalf("remove tag: %v", err)
	}

	counts, _ = tagRepo.ListWithCountFiltered(ctx, model.MediaFilter{})
	for _, tw := range counts {
		if tw.Name == "prunable" {
			t.Error("orphan tag still shown after RemoveFromJob")
		}
	}

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

	// Оборванная привязка — наследие данных, созданных до включения внешних
	// ключей, поэтому вставляем её при временно отключённой проверке.
	withoutForeignKeys(t, database, func() {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO job_tags (job_id, tag_id) VALUES ('ghost-job-id', ?)`, tag.ID); err != nil {
			t.Fatalf("insert orphan job_tag: %v", err)
		}
	})
	if _, err := database.ExecContext(ctx,
		`INSERT INTO tags (id, name, kind) VALUES ('orphan-tag-id', 'orphan-tag', 'plain')`); err != nil {
		t.Fatalf("insert orphan tag: %v", err)
	}

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

// withoutForeignKeys выполняет fn с отключённой проверкой внешних ключей.
// Пишущее соединение единственное, поэтому PRAGMA действует на нужное.
func withoutForeignKeys(t *testing.T, database *db.DB, fn func()) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `PRAGMA foreign_keys=off`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	defer func() {
		if _, err := database.ExecContext(ctx, `PRAGMA foreign_keys=on`); err != nil {
			t.Fatalf("re-enable foreign keys: %v", err)
		}
	}()
	fn()
}

// ── Медиатека: фильтрация и пагинация ────────────────────────────────────────

// seedMedia создаёт n завершённых заданий с файлами указанного типа.
func seedMedia(t *testing.T, database *db.DB, kind string, n int) {
	t.Helper()
	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	ctx := context.Background()
	for i := range n {
		job := &model.Job{
			URL:    fmt.Sprintf("https://example.com/%s-%d", kind, i),
			Status: model.JobDone, Source: "web",
		}
		if err := jobs.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		item := &model.Item{
			JobID: job.ID, Kind: kind,
			Path: fmt.Sprintf("/data/%s-%d.bin", kind, i),
			Name: fmt.Sprintf("%s-%d.bin", kind, i),
		}
		if err := items.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJobRepo_FilterMediaPagination(t *testing.T) {
	database := openTestDB(t)
	jobs := repo.NewJobRepo(database)
	ctx := context.Background()

	seedMedia(t, database, "video", 5)

	total, err := jobs.CountMedia(ctx, model.MediaFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}

	first, err := jobs.FilterMedia(ctx, model.MediaFilter{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(first))
	}

	second, err := jobs.FilterMedia(ctx, model.MediaFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("page 2 len = %d, want 2", len(second))
	}
	if first[0].Item.ID == second[0].Item.ID {
		t.Error("offset ignored: second page repeats the first")
	}

	last, err := jobs.FilterMedia(ctx, model.MediaFilter{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(last) != 1 {
		t.Errorf("last page len = %d, want 1", len(last))
	}
}

// Поиск должен находить и по имени тега — это же поведение используется в Telegram.
func TestJobRepo_FilterMediaByTagText(t *testing.T) {
	database := openTestDB(t)
	jobs := repo.NewJobRepo(database)
	tags := repo.NewTagRepo(database)
	items := repo.NewItemRepo(database)
	ctx := context.Background()

	job := &model.Job{URL: "https://example.com/x", Status: model.JobDone, Source: "web"}
	if err := jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := items.Create(ctx, &model.Item{
		JobID: job.ID, Kind: "video", Path: "/data/x.mp4", Name: "x.mp4",
	}); err != nil {
		t.Fatal(err)
	}
	tag, _ := tags.Upsert(ctx, "лекции")
	if err := tags.AddToJob(ctx, job.ID, tag.ID); err != nil {
		t.Fatal(err)
	}

	found, err := jobs.FilterMedia(ctx, model.MediaFilter{Query: "лекци"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("search by tag name found %d rows, want 1", len(found))
	}
}

// Облако тегов должно учитывать выбранный тип файла.
func TestTagRepo_CountsRespectKind(t *testing.T) {
	database := openTestDB(t)
	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	tags := repo.NewTagRepo(database)
	ctx := context.Background()

	mk := func(kind, tagName string) {
		job := &model.Job{URL: "https://example.com/" + kind, Status: model.JobDone, Source: "web"}
		if err := jobs.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		if err := items.Create(ctx, &model.Item{
			JobID: job.ID, Kind: kind,
			Path: "/data/" + kind + ".bin", Name: kind + ".bin",
		}); err != nil {
			t.Fatal(err)
		}
		tag, _ := tags.Upsert(ctx, tagName)
		if err := tags.AddToJob(ctx, job.ID, tag.ID); err != nil {
			t.Fatal(err)
		}
	}
	mk("video", "только-видео")
	mk("audio", "только-аудио")

	counts, err := tags.ListWithCountFiltered(ctx, model.MediaFilter{Kind: "audio"})
	if err != nil {
		t.Fatalf("tag counts: %v", err)
	}
	for _, c := range counts {
		if c.Name == "только-видео" {
			t.Error("video-only tag must not appear when filtering by audio")
		}
	}
}

// TestTagRepo_PruneOrphansKeepsEmptyCollection проверяет, что переиндексация не
// уничтожает только что созданную пустую подборку. Create заводит коллекцию без
// тега — тег появляется лишь в AddJobs, — поэтому прежнее условие «коллекции без
// активных заданий» накрывало любую пустую коллекцию, и пользователь терял её
// без единого сообщения. Существующий тест этого не ловил, так как коллекций
// вообще не заводил.
func TestTagRepo_PruneOrphansKeepsEmptyCollection(t *testing.T) {
	database := openTestDB(t)
	tagRepo := repo.NewTagRepo(database)
	colRepo := repo.NewCollectionRepo(database)
	ctx := context.Background()

	created, err := colRepo.Create(ctx, "Отпуск 2026")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	if _, _, nCollections, err := tagRepo.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	} else if nCollections != 0 {
		t.Errorf("удалено коллекций: %d; пустая подборка — не мусор", nCollections)
	}

	list, err := colRepo.List(ctx)
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	for _, c := range list {
		if c.ID == created.ID {
			return
		}
	}
	t.Fatal("пустая коллекция удалена переиндексацией")
}

// TestItemRepo_CreateReassignsJobOnPathConflict проверяет, что файл по уже
// занятому пути переходит к новому заданию. Раньше ON CONFLICT обновлял всё
// кроме job_id: файл оставался за первым заданием, второе получало «готово» с
// нулём файлов и выпадало из обеих половин запроса медиатеки — строку нельзя
// было ни увидеть, ни скрыть, ни удалить.
func TestItemRepo_CreateReassignsJobOnPathConflict(t *testing.T) {
	database := openTestDB(t)
	jobRepo := repo.NewJobRepo(database)
	itemRepo := repo.NewItemRepo(database)
	ctx := context.Background()

	jobA := &model.Job{URL: "https://example.com/a", Status: model.JobDone, Source: "web"}
	if err := jobRepo.Create(ctx, jobA); err != nil {
		t.Fatalf("create job A: %v", err)
	}
	jobB := &model.Job{URL: "https://example.com/b", Status: model.JobDone, Source: "web"}
	if err := jobRepo.Create(ctx, jobB); err != nil {
		t.Fatalf("create job B: %v", err)
	}

	const path = "/data/Интервью.mp4"
	first := &model.Item{JobID: jobA.ID, Kind: "video", Path: path, Name: "Интервью.mp4", Size: 10}
	if err := itemRepo.Create(ctx, first); err != nil {
		t.Fatalf("create item A: %v", err)
	}
	second := &model.Item{JobID: jobB.ID, Kind: "video", Path: path, Name: "Интервью.mp4", Size: 20}
	if err := itemRepo.Create(ctx, second); err != nil {
		t.Fatalf("create item B: %v", err)
	}

	ofB, err := itemRepo.ListByJobID(ctx, jobB.ID)
	if err != nil {
		t.Fatalf("list by job B: %v", err)
	}
	if len(ofB) != 1 {
		t.Fatalf("у второго задания %d файлов, ожидался 1 — задание исчезло бы из медиатеки", len(ofB))
	}
	if ofB[0].Size != 20 {
		t.Errorf("размер не обновлён: %d", ofB[0].Size)
	}

	ofA, err := itemRepo.ListByJobID(ctx, jobA.ID)
	if err != nil {
		t.Fatalf("list by job A: %v", err)
	}
	if len(ofA) != 0 {
		t.Errorf("файл остался и за первым заданием: %d", len(ofA))
	}
}
