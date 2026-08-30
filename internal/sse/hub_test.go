package sse_test

import (
	"testing"
	"time"

	"github.com/dr-duke/talmorGo/internal/sse"
)

// recv ждёт очередной набор тем.
func recv(t *testing.T, ch <-chan []sse.Topic, within time.Duration) []sse.Topic {
	t.Helper()
	select {
	case topics := <-ch:
		return topics
	case <-time.After(within):
		t.Fatal("не дождались публикации")
		return nil
	}
}

func TestHub_DeliversTopics(t *testing.T) {
	h := sse.NewWithWindow(10 * time.Millisecond)
	ch, unsub := h.Subscribe()
	defer unsub()

	h.Publish(sse.TopicQueue)

	got := recv(t, ch, time.Second)
	if len(got) != 1 || got[0] != sse.TopicQueue {
		t.Errorf("получено %v, ожидалось [queue]", got)
	}
}

// Публикации внутри окна склеиваются в одну рассылку — ради этого окно и нужно.
func TestHub_CoalescesWithinWindow(t *testing.T) {
	h := sse.NewWithWindow(50 * time.Millisecond)
	ch, unsub := h.Subscribe()
	defer unsub()

	for range 20 {
		h.Publish(sse.TopicLibrary)
		h.Publish(sse.TopicQueue)
	}

	got := recv(t, ch, time.Second)
	if len(got) != 2 {
		t.Fatalf("получено %v, ожидались обе темы одним набором", got)
	}
	if got[0] != sse.TopicLibrary || got[1] != sse.TopicQueue {
		t.Errorf("темы = %v, ожидался отсортированный [library queue]", got)
	}

	// Больше рассылок быть не должно: 40 публикаций схлопнулись в одну.
	select {
	case extra := <-ch:
		t.Errorf("лишняя рассылка: %v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

// После окна следующая публикация начинает новый цикл.
func TestHub_PublishesAgainAfterWindow(t *testing.T) {
	h := sse.NewWithWindow(10 * time.Millisecond)
	ch, unsub := h.Subscribe()
	defer unsub()

	h.Publish(sse.TopicTags)
	recv(t, ch, time.Second)

	h.Publish(sse.TopicCollections)
	got := recv(t, ch, time.Second)
	if len(got) != 1 || got[0] != sse.TopicCollections {
		t.Errorf("вторая рассылка = %v, ожидалось [collections]", got)
	}
}

func TestHub_MultipleSubscribers(t *testing.T) {
	h := sse.NewWithWindow(10 * time.Millisecond)
	a, unsubA := h.Subscribe()
	defer unsubA()
	b, unsubB := h.Subscribe()
	defer unsubB()

	h.Publish(sse.TopicLibrary)

	if got := recv(t, a, time.Second); len(got) != 1 {
		t.Errorf("подписчик A получил %v", got)
	}
	if got := recv(t, b, time.Second); len(got) != 1 {
		t.Errorf("подписчик B получил %v", got)
	}
}

// Отписка не должна приводить к записи в закрытый канал.
func TestHub_UnsubscribeIsSafe(t *testing.T) {
	h := sse.NewWithWindow(5 * time.Millisecond)
	_, unsub := h.Subscribe()
	unsub()
	unsub() // повторная отписка безопасна

	h.Publish(sse.TopicLibrary)
	time.Sleep(50 * time.Millisecond) // паники быть не должно
}

// Пустая публикация ничего не рассылает.
func TestHub_PublishNothing(t *testing.T) {
	h := sse.NewWithWindow(5 * time.Millisecond)
	ch, unsub := h.Subscribe()
	defer unsub()

	h.Publish()

	select {
	case got := <-ch:
		t.Errorf("неожиданная рассылка: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
