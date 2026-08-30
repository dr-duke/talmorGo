package audio_test

import (
	"testing"

	"github.com/dr-duke/talmorGo/internal/audio"
)

func TestParseTitle(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantArtist string
		wantTitle  string
	}{
		{
			name:       "обычное тире",
			in:         "Ludovico Einaudi - Nuvole Bianche",
			wantArtist: "Ludovico Einaudi",
			wantTitle:  "Nuvole Bianche",
		},
		{
			name:       "длинное тире",
			in:         "Пикник — Иероглиф",
			wantArtist: "Пикник",
			wantTitle:  "Иероглиф",
		},
		{
			name:       "среднее тире и служебная пометка",
			in:         "Some Band – Some Song (Official Video)",
			wantArtist: "Some Band",
			wantTitle:  "Some Song",
		},
		{
			name:       "пометка в квадратных скобках",
			in:         "Artist - Track [Official Music Video]",
			wantArtist: "Artist",
			wantTitle:  "Track",
		},
		{
			name:       "качество в названии",
			in:         "Artist - Track (4K)",
			wantArtist: "Artist",
			wantTitle:  "Track",
		},
		{
			name:       "русская запись с кавычками",
			in:         "Аквариум «Город золотой»",
			wantArtist: "Аквариум",
			wantTitle:  "Город золотой",
		},
		{
			name:       "несколько тире: делим по первому",
			in:         "Artist - Track - Live Version",
			wantArtist: "Artist",
			wantTitle:  "Track - Live Version",
		},
		{
			name:       "без разделителя — всё в название",
			in:         "Лекция про устройство планировщика Go",
			wantArtist: "",
			wantTitle:  "Лекция про устройство планировщика Go",
		},
		{
			name:       "расширение отбрасывается",
			in:         "Artist - Track.mp4",
			wantArtist: "Artist",
			wantTitle:  "Track",
		},
		{
			name:       "артефакты yt-dlp убираются",
			in:         "Artist - Track [dQw4w9WgXcQ].mp4",
			wantArtist: "Artist",
			wantTitle:  "Track",
		},
		{
			name:       "подчёркивания вместо пробелов",
			in:         "Big_Buck_Bunny_360_10s_1MB.mp4",
			wantArtist: "",
			wantTitle:  "Big Buck Bunny 360 10s 1MB",
		},
		{
			name:       "дефис без пробелов не разделитель",
			in:         "Nu-Metal Mix",
			wantArtist: "",
			wantTitle:  "Nu-Metal Mix",
		},
		{
			name:       "пустая часть — не разделяем",
			in:         "- Track",
			wantArtist: "",
			wantTitle:  "Track",
		},
		{
			name:       "пустое имя",
			in:         "   ",
			wantArtist: "",
			wantTitle:  "",
		},
		{
			name:       "кавычки вокруг названия снимаются",
			in:         `Artist - "Track"`,
			wantArtist: "Artist",
			wantTitle:  "Track",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			artist, title := audio.ParseTitle(c.in)
			if artist != c.wantArtist {
				t.Errorf("исполнитель = %q, ожидался %q", artist, c.wantArtist)
			}
			if title != c.wantTitle {
				t.Errorf("композиция = %q, ожидалась %q", title, c.wantTitle)
			}
		})
	}
}

// Домен источника в тегах появляться не должен.
func TestTrackMeta_NoSourceMention(t *testing.T) {
	meta := audio.TrackMeta("Artist - Track [dQw4w9WgXcQ].mp4")
	if meta.Artist != "Artist" || meta.Title != "Track" {
		t.Fatalf("теги = %+v", meta)
	}
	if meta.Album != "" || meta.Year != "" || meta.Genre != "" {
		t.Errorf("лишние поля заполнены: %+v", meta)
	}
}

// Когда исполнителя выделить нельзя, название целиком идёт в композицию.
func TestTrackMeta_FallsBackToTitle(t *testing.T) {
	meta := audio.TrackMeta("Подкаст про Go, выпуск 12.webm")
	if meta.Artist != "" {
		t.Errorf("исполнитель = %q, ожидался пустой", meta.Artist)
	}
	if meta.Title != "Подкаст про Go, выпуск 12" {
		t.Errorf("композиция = %q", meta.Title)
	}
}
