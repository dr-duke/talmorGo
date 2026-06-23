package model

import "time"

type JobStatus string

const (
	JobPending  JobStatus = "pending"
	JobRunning  JobStatus = "running"
	JobRetrying JobStatus = "retrying"
	JobDone     JobStatus = "done"
	JobFailed   JobStatus = "failed"
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
}

// DisplayName возвращает title, если он уже известен, иначе URL.
func (j *Job) DisplayName() string {
	if j.Title != "" {
		return j.Title
	}
	return j.URL
}

type File struct {
	ID        string
	Path      string
	Name      string
	Size      int64
	CreatedAt time.Time
	DeletedAt *time.Time
}

// DeletedFile — представление удалённого файла для API-эндпоинта.
type DeletedFile struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	OriginalURL string     `json:"original_url"`
	DeletedAt   time.Time  `json:"deleted_at"`
}

type Token struct {
	Token     string
	FileID    string
	CreatedAt time.Time
}
