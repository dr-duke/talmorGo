// Package sse реализует простой pub/sub-хаб для Server-Sent Events.
// Воркер и обработчики вызывают Broadcast() при изменении состояния заданий;
// подключённые браузерные вкладки получают событие и перезапрашивают список.
package sse

import "sync"

// Hub рассылает сигнал об изменениях всем подключённым SSE-клиентам.
type Hub struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

func New() *Hub {
	return &Hub{clients: make(map[chan struct{}]struct{})}
}

// Subscribe регистрирует нового клиента.
// Возвращает канал (буферизован на 1) и функцию отписки.
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		close(ch)
	}
}

// Broadcast отправляет сигнал всем подключённым клиентам (non-blocking).
// Если клиент не успел прочитать предыдущий сигнал — новый не дублируется.
func (h *Hub) Broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
