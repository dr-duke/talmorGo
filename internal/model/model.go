package model

import (
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type JobStatus string

const (
	JobChecking  JobStatus = "checking"  // временный: yt-dlp проверяет не плейлист ли URL
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobRetrying  JobStatus = "retrying"
	JobDone      JobStatus = "done"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
	JobImported  JobStatus = "imported"  // файл найден сканером на диске, не скачан ботом
)

type Job struct {
	ID            string
	URL           string
	Status        JobStatus
	Title         string
	FileID        string
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

// Domain извлекает hostname из URL задания.
func (j *Job) Domain() string {
	u, err := url.Parse(j.URL)
	if err != nil || u.Host == "" {
		return j.URL
	}
	return u.Hostname()
}

type File struct {
	ID        string
	JobID     string
	Path      string
	Name      string
	Size      int64
	CreatedAt time.Time
	DeletedAt *time.Time
	LostAt    *time.Time
}

func (f *File) IsLost() bool    { return f.LostAt != nil }
func (f *File) IsDeleted() bool { return f.DeletedAt != nil }
func (f *File) IsAvailable() bool {
	return f.DeletedAt == nil && f.LostAt == nil
}

func (f *File) IsVideo() bool {
	switch strings.ToLower(filepath.Ext(f.Name)) {
	case ".mp4", ".mkv", ".webm", ".avi", ".mov", ".ts", ".m4v", ".wmv":
		return true
	}
	return false
}

// MediaItem — объединённое представление задания и (опционально) файла.
type MediaItem struct {
	Job  *Job
	File *File  // nil пока файл не скачан
	Tags []string
}

// EffectiveStatus возвращает статус с учётом состояния файла и флага скрытия.
func (m *MediaItem) EffectiveStatus() string {
	if m.Job.Hidden {
		return "hidden"
	}
	if m.File != nil && m.File.LostAt != nil {
		return "missing"
	}
	if m.File != nil && m.File.DeletedAt != nil && (m.Job.Status == JobDone || m.Job.Status == JobImported) {
		return "deleted"
	}
	return string(m.Job.Status)
}

// DisplayTitle возвращает имя файла или заголовок задания.
func (m *MediaItem) DisplayTitle() string {
	if m.File != nil && m.File.IsAvailable() {
		return m.File.Name
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

// DeletedFile — для GET /files/deleted API.
type DeletedFile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	OriginalURL string    `json:"original_url"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type Token struct {
	Token     string
	FileID    string
	CreatedAt time.Time
}

type Tag struct {
	ID   string
	Name string
}

// CookieRecord — куки одного домена (Netscape-формат), хранимые в БД.
type CookieRecord struct {
	Domain    string
	Content   string // Netscape-строки для этого домена
	UpdatedAt string
}
