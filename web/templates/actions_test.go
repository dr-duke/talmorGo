package templates

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/model"
)

// Разметка объявляет намерения через data-action, обработчики регистрируются в
// JS-модулях. Половины лежат врозь, поэтому сверяем их тестом: опечатка в имени
// действия иначе превращается в молча неработающую кнопку — так однажды и «умер»
// экран коллекций.

var actionAttrs = map[string]string{
	"data-action":        "click",
	"data-action-change": "change",
	"data-action-input":  "input",
}

// render собирает HTML всех компонентов, где встречаются действия.
func render(t *testing.T) string {
	t.Helper()

	now := time.Now()
	job := &model.Job{
		ID: "job-1", URL: "https://example.com/v", Title: "Видео.mp4",
		Status: model.JobDone, Source: "web", CreatedAt: now, UpdatedAt: now,
	}
	audioItem := &model.Item{
		ID: "item-1", JobID: job.ID, Kind: "audio",
		Path: "/data/track.mp3", Name: "track.mp3", Size: 10,
	}
	videoItem := &model.Item{
		ID: "item-2", JobID: job.ID, Kind: "video",
		Path: "/data/clip.mp4", Name: "clip.mp4", Size: 20,
	}
	hidden := &model.Job{
		ID: "job-2", URL: "https://example.com/h", Status: model.JobDone,
		Source: "web", Hidden: true, CreatedAt: now, UpdatedAt: now,
	}
	failed := &model.Job{
		ID: "job-3", URL: "https://example.com/f", Status: model.JobFailed,
		Source: "web", CreatedAt: now, UpdatedAt: now,
	}
	retrying := &model.Job{
		ID: "job-4", URL: "https://example.com/r", Status: model.JobRetrying,
		Source: "web", CreatedAt: now, UpdatedAt: now, NextRetryAt: &now,
	}

	cols := []*model.Collection{{ID: "col-1", Name: "Лекции", ItemCount: 3}}

	// Страница со всеми разновидностями строк: аудио, видео, скрытая, упавшая.
	page := &library.Page{
		Items: []*model.MediaItem{
			{Job: job, Item: audioItem, Tags: []string{"тег"}},
			{Job: job, Item: videoItem},
			{Job: hidden},
			{Job: failed},
			{Job: retrying},
		},
		Total:  99, // больше, чем строк — покажется кнопка догрузки
		Filter: model.MediaFilter{Limit: 5},
	}

	components := []templ.Component{
		Index("", "TalmorGo", cols),
		SidebarNav(cols),
		ItemList(page),
		ItemRows(page),
		TagCloud([]*model.TagWithCount{
			{Name: "тег", Count: 2},
			{Name: "Лекции", Count: 3, IsCollection: true},
			{Name: "a", Count: 1}, {Name: "b", Count: 1},
			{Name: "c", Count: 1}, {Name: "d", Count: 1}, // > 5 → кнопка «ещё»
		}, "тег"),
		QueueItems(
			[]*model.Job{failed, retrying, hidden},
			[]*model.Operation{
				{ID: "op-1", Kind: "bulk_tag", Status: model.OpDone, Title: "Тег"},
				{ID: "op-2", Kind: "bulk_tag", Status: model.OpFailed, Title: "Тег", Error: "ошибка"},
			},
		),
		ActionBar(),
	}

	var sb strings.Builder
	for _, c := range components {
		if err := c.Render(context.Background(), &sb); err != nil {
			t.Fatalf("render: %v", err)
		}
	}
	return sb.String()
}

var actionRe = regexp.MustCompile(`data-action(?:-change|-input)?="([a-z-]+)"`)

// usedActions возвращает действия, встречающиеся в разметке, по типу события.
func usedActions(html string) map[string]map[string]bool {
	used := map[string]map[string]bool{"click": {}, "change": {}, "input": {}}
	for attr, kind := range actionAttrs {
		re := regexp.MustCompile(attr + `="([a-z-]+)"`)
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			used[kind][m[1]] = true
		}
	}
	return used
}

// registeredActions разбирает JS-модули и собирает зарегистрированные действия.
func registeredActions(t *testing.T) map[string]map[string]bool {
	t.Helper()

	dir := filepath.Join("..", "static", "js")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("читаем каталог модулей: %v", err)
	}

	fnKind := map[string]string{
		"register":       "click",
		"registerChange": "change",
		"registerInput":  "input",
	}
	keyRe := regexp.MustCompile(`(?m)^\s*'?([a-zA-Z][a-zA-Z0-9-]*)'?\s*:`)

	out := map[string]map[string]bool{"click": {}, "change": {}, "input": {}}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("читаем %s: %v", e.Name(), err)
		}
		text := string(src)

		for fn, kind := range fnKind {
			for _, body := range callBodies(text, fn+"({") {
				for _, m := range keyRe.FindAllStringSubmatch(body, -1) {
					out[kind][m[1]] = true
				}
			}
		}
	}
	return out
}

// callBodies выделяет тела вызовов вида register({ … }) с учётом вложенных скобок.
func callBodies(text, prefix string) []string {
	var bodies []string
	for idx := 0; ; {
		i := strings.Index(text[idx:], prefix)
		if i < 0 {
			return bodies
		}
		start := idx + i + len(prefix)
		depth := 1
		j := start
		for ; j < len(text) && depth > 0; j++ {
			switch text[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		bodies = append(bodies, text[start:j])
		idx = j
	}
}

func TestTemplateActionsAreHandled(t *testing.T) {
	used := usedActions(render(t))
	registered := registeredActions(t)

	total := 0
	for kind, names := range used {
		for name := range names {
			total++
			if !registered[kind][name] {
				t.Errorf("разметка ссылается на действие %q (%s), которого нет ни в одном JS-модуле", name, kind)
			}
		}
	}
	if total < 20 {
		t.Errorf("найдено всего %d действий — проверка вряд ли покрывает разметку", total)
	}
}

// Обратная сторона: зарегистрированный обработчик без разметки — мёртвый код.
func TestNoUnusedActionHandlers(t *testing.T) {
	used := usedActions(render(t))
	registered := registeredActions(t)

	// Разметку выпадающего списка коллекций строит collections.js, а не шаблон,
	// поэтому этих двух действий в templ-файлах нет по построению.
	ignore := map[string]bool{
		"coll-add":    true,
		"coll-create": true,
	}

	for kind, names := range registered {
		for name := range names {
			if ignore[name] || used[kind][name] {
				continue
			}
			t.Errorf("обработчик %q (%s) зарегистрирован, но в разметке не используется", name, kind)
		}
	}
}

// В шаблонах не должно остаться inline-обработчиков: они и приводили к вызову
// глобальных функций, которых уже нет.
func TestNoInlineHandlers(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// onerror у favicon допустим: он не вызывает наш код.
	allowed := regexp.MustCompile(`onerror="this\.style\.visibility='hidden'"`)
	inline := regexp.MustCompile(`on(click|change|input|submit)="`)

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".templ") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := allowed.ReplaceAllString(string(src), "")
		if m := inline.FindString(text); m != "" {
			t.Errorf("%s содержит inline-обработчик %s…", e.Name(), m)
		}
	}
}
