package model

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ytdlpID matches yt-dlp video IDs in filenames, e.g. " [CRbLJq6Pgew]" before the extension.
var ytdlpID = regexp.MustCompile(`\s*\[[A-Za-z0-9_-]{6,15}\](\.[^.]+)$`)

// ytdlpFmt matches yt-dlp format codes in filenames, e.g. ".f140" before the extension.
var ytdlpFmt = regexp.MustCompile(`\.f\d{3,4}(\.[^.]+)$`)

// cleanFileName strips yt-dlp artifacts (video IDs, format codes) from file names.
func cleanFileName(name string) string {
	strip := func(s string, m []int) string {
		ext := s[m[2]:]
		base := strings.TrimRight(s[:m[0]], " \t")
		return base + ext
	}
	if m := ytdlpID.FindStringSubmatchIndex(name); m != nil {
		name = strip(name, m)
	}
	if m := ytdlpFmt.FindStringSubmatchIndex(name); m != nil {
		name = strip(name, m)
	}
	return strings.TrimSpace(name)
}

type JobStatus string

const (
	JobChecking  JobStatus = "checking"  // yt-dlp проверяет, не плейлист ли URL
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobRetrying  JobStatus = "retrying"
	JobDone      JobStatus = "done"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
	JobImported  JobStatus = "imported" // файл найден сканером, не скачан ботом
)

type Job struct {
	ID            string
	URL           string
	Status        JobStatus
	Title         string
	Error         string
	Source        string // "web" | "telegram"
	ChatID        int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RetryCount    int
	NextRetryAt   *time.Time
	FirstFailedAt *time.Time
	TgMessageID   int64
	Hidden        bool
}

func (j *Job) DisplayName() string {
	if j.Title != "" {
		return j.Title
	}
	return j.URL
}

func (j *Job) Domain() string {
	u, err := url.Parse(j.URL)
	if err != nil || u.Host == "" {
		return j.URL
	}
	return u.Hostname()
}

// OpStatus — статус фоновой операции.
type OpStatus string

const (
	OpPending OpStatus = "pending"
	OpRunning OpStatus = "running"
	OpDone    OpStatus = "done"
	OpFailed  OpStatus = "failed"
)

// Operation — пакетная фоновая операция (bulk_tag / bulk_hide / bulk_meta и др.).
type Operation struct {
	ID         string
	Kind       string
	Status     OpStatus
	Title      string
	Payload    string // JSON-блоб с параметрами операции
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      string
}

// AudioMeta — ID3-метаданные аудиоэлемента.
type AudioMeta struct {
	Title  string
	Artist string
	Album  string
	Year   string
	Genre  string
}

// Item — унифицированный медиаэлемент (видео или аудио).
type Item struct {
	ID        string
	JobID     string
	Kind      string // "video" | "audio"
	Path      string
	Name      string
	Size      int64
	Duration  int // секунды
	Meta      AudioMeta
	CreatedAt time.Time
	DeletedAt *time.Time
	LostAt    *time.Time
}

func (i *Item) IsLost() bool      { return i.LostAt != nil }
func (i *Item) IsDeleted() bool   { return i.DeletedAt != nil }
func (i *Item) IsAvailable() bool { return i.DeletedAt == nil && i.LostAt == nil }
func (i *Item) IsAudio() bool     { return i.Kind == "audio" }
func (i *Item) IsVideo() bool     { return i.Kind == "video" }

// MediaItem — объединённое представление задания и (опционально) медиаэлемента.
type MediaItem struct {
	Job  *Job
	Item *Item  // nil пока файл не скачан
	Tags []string
}

// EffectiveStatus возвращает статус с учётом состояния элемента и флага скрытия.
func (m *MediaItem) EffectiveStatus() string {
	if m.Job.Hidden {
		return "hidden"
	}
	if m.Item != nil && m.Item.LostAt != nil {
		return "missing"
	}
	if m.Item != nil && m.Item.DeletedAt != nil && (m.Job.Status == JobDone || m.Job.Status == JobImported) {
		return "deleted"
	}
	return string(m.Job.Status)
}

// DisplayTitle возвращает имя файла или заголовок задания.
func (m *MediaItem) DisplayTitle() string {
	if m.Item != nil && m.Item.IsAvailable() {
		return cleanFileName(m.Item.Name)
	}
	if m.Job.Title != "" {
		return m.Job.Title
	}
	u := m.Job.URL
	if len(u) > 70 {
		return u[:67] + "…"
	}
	return u
}

// DeletedItem — для GET /items/deleted API.
type DeletedItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	OriginalURL string    `json:"original_url"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type Token struct {
	Token     string
	ItemID    string
	CreatedAt time.Time
}

type Tag struct {
	ID   string
	Name string
	Kind string // "plain" | "collection"
}

// Collection — именованная коллекция (имя совпадает с именем тега kind='collection').
type Collection struct {
	ID        string
	Name      string
	CreatedAt time.Time
	ItemCount int // заполняется репозиторием
}

// TagWithCount — тег с количеством привязанных заданий.
type TagWithCount struct {
	Name         string
	Count        int
	IsCollection bool
}

// MediaFilter — параметры серверной фильтрации медиатеки.
type MediaFilter struct {
	Query string   // текстовый поиск (имя файла, URL, заголовок)
	Kind  string   // "" | "video" | "audio"
	Tags  []string // AND-пересечение тегов (включая коллекции)
	Limit int      // максимум строк; 0 = без ограничений
}

// CookieRecord — куки одного домена (Netscape-формат).
type CookieRecord struct {
	Domain    string
	Content   string
	UpdatedAt string
}
