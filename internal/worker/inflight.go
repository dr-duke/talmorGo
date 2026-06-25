package worker

import "sync"

// InFlightPaths хранит пути файлов, которые воркер получил из yt-dlp stdout
// (after_move:filename) но ещё не успел записать в БД.
// Сканер проверяет этот набор, чтобы не импортировать файлы в процессе закачки.
type InFlightPaths struct {
	mu    sync.RWMutex
	paths map[string]struct{}
}

func NewInFlightPaths() *InFlightPaths {
	return &InFlightPaths{paths: make(map[string]struct{})}
}

func (s *InFlightPaths) Add(path string) {
	s.mu.Lock()
	s.paths[path] = struct{}{}
	s.mu.Unlock()
}

func (s *InFlightPaths) Remove(path string) {
	s.mu.Lock()
	delete(s.paths, path)
	s.mu.Unlock()
}

func (s *InFlightPaths) Contains(path string) bool {
	s.mu.RLock()
	_, ok := s.paths[path]
	s.mu.RUnlock()
	return ok
}
