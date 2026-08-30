// Package sse реализует pub/sub-хаб для Server-Sent Events.
//
// Хаб рассылает не «что-то изменилось», а конкретные темы: браузер
// перезапрашивает только затронутую часть страницы. Публикации склеиваются в
// окне ожидания — при загрузке плейлиста из полусотни видео вкладка получает
// несколько обновлений вместо полусотни полных перерисовок списка.
package sse

import (
	"slices"
	"sync"
	"time"
)

// Topic — область интерфейса, которую нужно обновить.
type Topic string

const (
	TopicLibrary     Topic = "library"     // список медиатеки
	TopicQueue       Topic = "queue"       // очередь заданий и фоновых операций
	TopicTags        Topic = "tags"        // облако тегов
	TopicCollections Topic = "collections" // список коллекций в сайдбаре
)

// DefaultWindow — окно склейки публикаций.
const DefaultWindow = 300 * time.Millisecond

type Hub struct {
	mu      sync.Mutex
	clients map[chan []Topic]struct{}
	pending map[Topic]struct{}
	timer   *time.Timer
	window  time.Duration
}

func New() *Hub { return NewWithWindow(DefaultWindow) }

// NewWithWindow позволяет задать окно склейки (используется в тестах).
func NewWithWindow(window time.Duration) *Hub {
	return &Hub{
		clients: make(map[chan []Topic]struct{}),
		pending: make(map[Topic]struct{}),
		window:  window,
	}
}

// Subscribe регистрирует клиента. Возвращает канал наборов тем и функцию отписки.
func (h *Hub) Subscribe() (<-chan []Topic, func()) {
	ch := make(chan []Topic, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.clients, ch)
			close(ch)
			h.mu.Unlock()
		})
	}
}

// Publish помечает темы изменёнными. Рассылка произойдёт не позже чем через
// окно склейки; повторные публикации той же темы внутри окна схлопываются.
func (h *Hub) Publish(topics ...Topic) {
	if len(topics) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range topics {
		h.pending[t] = struct{}{}
	}
	if h.timer == nil {
		h.timer = time.AfterFunc(h.window, h.flush)
	}
}

// flush рассылает накопленные темы всем подписчикам.
func (h *Hub) flush() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.timer = nil
	if len(h.pending) == 0 {
		return
	}
	topics := make([]Topic, 0, len(h.pending))
	for t := range h.pending {
		topics = append(topics, t)
	}
	clear(h.pending)
	// Стабильный порядок: удобнее и в логах, и в тестах.
	slices.Sort(topics)

	// Отправка под мьютексом: гарантирует, что отписавшийся клиент
	// не получит запись в уже закрытый канал.
	for ch := range h.clients {
		select {
		case ch <- topics:
		default: // клиент не успевает читать — пропускаем, следующий Publish его догонит
		}
	}
}
