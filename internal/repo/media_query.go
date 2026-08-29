package repo

import (
	"fmt"
	"strings"

	"github.com/dr-duke/talmorGo/internal/model"
)

// Единая точка сборки запросов медиатеки.
//
// Медиатека — это объединение двух наборов строк:
//   - по строке на каждый скачанный файл (jobs ⋈ items);
//   - по строке на каждое задание без файлов (в работе, упало, отменено).
//
// Раньше эти запросы (плюс построитель WHERE) были размножены по ListMedia,
// SearchMedia, FilterMedia, CountMedia и ListWithCountFiltered. Здесь они
// собираются из общих кирпичей, поэтому новое условие фильтра добавляется
// в одном месте.

// jobColumns — колонки задания, общие для обеих половин UNION.
const jobColumns = `j.id, j.url, j.status, j.title,
	j.error, j.source, j.chat_id,
	j.created_at, j.updated_at, j.retry_count, j.next_retry_at, j.first_failed_at,
	j.hidden`

// itemColumns — колонки медиаэлемента.
const itemColumns = `i.id, i.kind, i.name, i.size, i.path, i.duration,
	i.title, i.artist, i.album, i.year, i.genre,
	i.created_at, i.deleted_at, i.lost_at`

// nullItemColumns — те же 14 колонок для строк заданий без файлов.
const nullItemColumns = `NULL, NULL, NULL, NULL, NULL, NULL,
	NULL, NULL, NULL, NULL, NULL,
	NULL, NULL, NULL`

// tagsColumn — теги задания одной строкой, разделитель '|'.
const tagsColumn = `(SELECT GROUP_CONCAT(t2.name,'|')
	 FROM job_tags jt2 JOIN tags t2 ON t2.id=jt2.tag_id WHERE jt2.job_id=j.id)`

// pendingStatuses — статусы заданий, которые показываются в медиатеке до появления файла.
const pendingStatuses = `'checking','pending','running','retrying','failed','cancelled'`

// tagMatchSubquery — задание имеет тег, подходящий под LIKE-шаблон.
const tagMatchSubquery = `j.id IN (SELECT jt3.job_id FROM job_tags jt3
	 JOIN tags t3 ON t3.id=jt3.tag_id WHERE t3.name LIKE ?)`

// tagExactSubquery — задание помечено конкретным тегом.
const tagExactSubquery = `j.id IN (SELECT jt.job_id FROM job_tags jt
	 JOIN tags t ON t.id=jt.tag_id WHERE t.name=?)`

// mediaConds — условия фильтра, раздельно для строк с файлом и без.
type mediaConds struct {
	item []string
	job  []string
	// Аргументы соответствуют условиям в порядке добавления.
	itemArgs []any
	jobArgs  []any
}

// buildMediaConds раскладывает фильтр на условия для обеих половин запроса.
// Текстовый поиск покрывает имя файла, URL, заголовок задания и имена тегов.
func buildMediaConds(f model.MediaFilter) mediaConds {
	var c mediaConds

	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		c.item = append(c.item, "(i.name LIKE ? OR j.url LIKE ? OR j.title LIKE ? OR "+tagMatchSubquery+")")
		c.itemArgs = append(c.itemArgs, like, like, like, like)
		c.job = append(c.job, "(j.url LIKE ? OR j.title LIKE ? OR "+tagMatchSubquery+")")
		c.jobArgs = append(c.jobArgs, like, like, like)
	}

	if f.Kind != "" {
		c.item = append(c.item, "i.kind=?")
		c.itemArgs = append(c.itemArgs, f.Kind)
		// К строкам без файла тип не применим — они исключаются целиком (см. buildMediaQuery).
	}

	for _, tag := range f.Tags {
		c.item = append(c.item, tagExactSubquery)
		c.itemArgs = append(c.itemArgs, tag)
		c.job = append(c.job, tagExactSubquery)
		c.jobArgs = append(c.jobArgs, tag)
	}

	return c
}

func whereClause(base string, conds []string) string {
	if len(conds) == 0 {
		return " WHERE " + base
	}
	return " WHERE " + base + " AND " + strings.Join(conds, " AND ")
}

// includePendingRows сообщает, нужна ли половина запроса с заданиями без файлов.
// При фильтре по типу файла такие строки не показываются: у них нет kind.
func includePendingRows(f model.MediaFilter) bool { return f.Kind == "" }

// buildMediaQuery собирает запрос списка медиатеки и его аргументы.
func buildMediaQuery(f model.MediaFilter) (string, []any) {
	c := buildMediaConds(f)

	q := `SELECT ` + jobColumns + `, ` + itemColumns + `, ` + tagsColumn + ` AS tags,
		COALESCE(i.created_at, j.created_at) AS sort_ts
		FROM items i
		JOIN jobs j ON j.id = i.job_id` + whereClause("j.hidden=0", c.item)

	args := append([]any{}, c.itemArgs...)

	if includePendingRows(f) {
		q += `
		UNION ALL
		SELECT ` + jobColumns + `, ` + nullItemColumns + `, ` + tagsColumn + ` AS tags,
			j.created_at AS sort_ts
			FROM jobs j` +
			whereClause("j.hidden=0", append([]string{
				"j.status IN (" + pendingStatuses + ")",
				"NOT EXISTS (SELECT 1 FROM items WHERE job_id = j.id)",
			}, c.job...))
		args = append(args, c.jobArgs...)
	}

	q += "\n\t\tORDER BY sort_ts DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf("\n\t\tLIMIT %d", f.Limit)
		if f.Offset > 0 {
			q += fmt.Sprintf(" OFFSET %d", f.Offset)
		}
	}
	return q, args
}

// buildMediaCountQuery собирает счётчик строк для того же фильтра без Limit/Offset.
func buildMediaCountQuery(f model.MediaFilter) (string, []any) {
	c := buildMediaConds(f)

	inner := `SELECT i.id AS id FROM items i
		JOIN jobs j ON j.id = i.job_id` + whereClause("j.hidden=0", c.item)

	args := append([]any{}, c.itemArgs...)

	if includePendingRows(f) {
		inner += `
		UNION ALL
		SELECT j.id AS id FROM jobs j` +
			whereClause("j.hidden=0", append([]string{
				"j.status IN (" + pendingStatuses + ")",
				"NOT EXISTS (SELECT 1 FROM items WHERE job_id = j.id)",
			}, c.job...))
		args = append(args, c.jobArgs...)
	}

	return "SELECT COUNT(*) FROM (" + inner + ")", args
}

// buildTagCountQuery собирает запрос счётчиков тегов для текущего фильтра.
// В отличие от прежней версии учитывает и фильтр по типу файла.
func buildTagCountQuery(f model.MediaFilter) (string, []any) {
	c := buildMediaConds(f)

	// Счётчики считаются по заданиям, поэтому условия берём из «job»-половины,
	// а фильтр по типу превращаем в требование наличия файла нужного типа.
	conds := append([]string{}, c.job...)
	args := append([]any{}, c.jobArgs...)
	if f.Kind != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM items i WHERE i.job_id=j.id AND i.kind=?)")
		args = append(args, f.Kind)
	}

	q := `SELECT t.name, COUNT(DISTINCT j.id) AS cnt,
		       CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END AS is_coll
		FROM tags t
		JOIN job_tags jt ON jt.tag_id = t.id
		JOIN jobs j ON j.id = jt.job_id
		LEFT JOIN collections c ON c.name = t.name` +
		whereClause("j.hidden=0", conds) + `
		GROUP BY t.id
		HAVING cnt > 0
		ORDER BY is_coll DESC, cnt DESC, t.name ASC`

	return q, args
}
