package repo

import (
	"context"
	"strings"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteTagRepo struct {
	db *db.DB
}

func NewTagRepo(database *db.DB) TagRepo {
	return &sqliteTagRepo{db: database}
}

// Upsert возвращает существующий тег или создаёт новый.
func (r *sqliteTagRepo) Upsert(ctx context.Context, name string) (*model.Tag, error) {
	id := uuid.NewString()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tags (id, name) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`, id, name)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, name, kind FROM tags WHERE name=?`, name)
	var t model.Tag
	if err := row.Scan(&t.ID, &t.Name, &t.Kind); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *sqliteTagRepo) ListAll(ctx context.Context) ([]*model.Tag, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, kind FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []*model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind); err != nil {
			return nil, err
		}
		tags = append(tags, &t)
	}
	return tags, rows.Err()
}

func (r *sqliteTagRepo) AddToJob(ctx context.Context, jobID, tagID string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES (?, ?)`, jobID, tagID)
	return err
}

func (r *sqliteTagRepo) RemoveFromJob(ctx context.Context, jobID, tagName string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM job_tags WHERE job_id=? AND tag_id=(SELECT id FROM tags WHERE name=?)`,
		jobID, tagName); err != nil {
		return err
	}
	// Удаляем тег если он больше ни к чему не привязан (коллекции не трогаем).
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM tags WHERE name=? AND kind='plain'
		 AND id NOT IN (SELECT DISTINCT tag_id FROM job_tags)`,
		tagName)
	return err
}

// ListWithCountFiltered возвращает облако тегов для текущего фильтра медиатеки.
// Учитываются текстовый поиск, выбранные теги и тип файла: на вкладке «Аудио»
// счётчики считаются только по заданиям с аудиофайлами.
func (r *sqliteTagRepo) ListWithCountFiltered(ctx context.Context, f model.MediaFilter) ([]*model.TagWithCount, error) {
	q, args := buildTagCountQuery(f)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.TagWithCount
	for rows.Next() {
		var tw model.TagWithCount
		var isCol int
		if err := rows.Scan(&tw.Name, &tw.Count, &isCol); err != nil {
			return nil, err
		}
		tw.IsCollection = isCol == 1
		out = append(out, &tw)
	}
	return out, rows.Err()
}

func (r *sqliteTagRepo) BulkAddToJobs(ctx context.Context, tagID string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(jobIDs))
	args := make([]any, 0, len(jobIDs)*2)
	for i, id := range jobIDs {
		placeholders[i] = "(?, ?)"
		args = append(args, id, tagID)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES `+strings.Join(placeholders, ","),
		args...)
	return err
}

func (r *sqliteTagRepo) PruneOrphans(ctx context.Context) (nJobTags, nTags, nCollections int, err error) {
	exec := func(q string) (int, error) {
		res, e := r.db.ExecContext(ctx, q)
		if e != nil {
			return 0, e
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}

	// 1. job_tags → несуществующие jobs
	if nJobTags, err = exec(`DELETE FROM job_tags WHERE job_id NOT IN (SELECT id FROM jobs)`); err != nil {
		return
	}
	// 2. plain теги без привязанных заданий
	if nTags, err = exec(`DELETE FROM tags WHERE kind='plain' AND id NOT IN (SELECT DISTINCT tag_id FROM job_tags)`); err != nil {
		return
	}
	// 3. Коллекции не трогаем. Раньше здесь удалялись коллекции без заданий —
	// но Create заводит коллекцию вообще без тега (тег появляется только в
	// AddJobs), поэтому под условие подпадала любая только что созданная
	// пустая подборка: пользователь заводил её, нажимал «Переиндексировать» и
	// терял без единого сообщения. Коллекция — пользовательская сущность, а не
	// мусор: пустую удаляет только сам пользователь.
	nCollections = 0
	// 4. collection-теги без соответствующей записи в collections
	n, e := exec(`DELETE FROM tags WHERE kind='collection' AND name NOT IN (SELECT name FROM collections)`)
	nTags += n
	err = e
	return
}
