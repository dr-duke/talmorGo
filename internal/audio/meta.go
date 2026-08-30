package audio

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dr-duke/talmorGo/internal/model"
)

// Разбор названия видео в теги аудиодорожки.
//
// Источник скачивания в тегах не упоминается: раньше в поле «исполнитель»
// попадал домен, из-за чего вся извлечённая музыка в плеере оказывалась
// «от youtube.com». Теперь исполнитель извлекается из самого названия, а если
// это не удалось — поле остаётся пустым, а название целиком идёт в «композицию».

// noise — служебные пометки в скобках, которые в теги не нужны.
var noise = regexp.MustCompile(`(?i)[\(\[]\s*(` +
	`official\s+(music\s+)?(video|audio|clip)|music\s+video|` +
	`lyrics?(\s+video)?|audio\s+only|` +
	`full\s*hd|hd|hq|[48]k|\d{3,4}p|` +
	`официальн\w*\s+(клип|видео)|премьера\s+клипа|видеоклип` +
	`)\s*[\)\]]`)

// separators — тире, которым обычно разделяют исполнителя и название.
var separators = []string{" — ", " – ", " ‒ ", " ― ", " - ", " − "}

// quoted — русская запись вида: Исполнитель «Название».
var quoted = regexp.MustCompile(`^(.+?)\s*[«"]([^»"]+)[»"]\s*$`)

var spaces = regexp.MustCompile(`\s{2,}`)

// ParseTitle раскладывает название видео на исполнителя и композицию.
//
// Возвращает пустого исполнителя, когда разделить не удалось: выдумывать
// исполнителя хуже, чем оставить поле пустым — в плеере запись просто попадёт
// в «неизвестный исполнитель», а название останется читаемым.
func ParseTitle(raw string) (artist, title string) {
	name := clean(raw)
	if name == "" {
		return "", ""
	}

	// Явный разделитель проверяем первым: в записи «Artist - "Track"» тире
	// главнее кавычек, иначе оно утащило бы за собой имя исполнителя.
	for _, sep := range separators {
		before, after, found := strings.Cut(name, sep)
		if !found {
			continue
		}
		a, t := strings.TrimSpace(before), strings.TrimSpace(after)
		// Обе части должны быть осмысленными: иначе это тире внутри названия.
		if a == "" || t == "" {
			continue
		}
		return unquote(a), unquote(t)
	}

	// «Исполнитель «Название»» — распространённая запись у русскоязычных клипов.
	if m := quoted.FindStringSubmatch(name); m != nil {
		if a, t := strings.TrimSpace(m[1]), strings.TrimSpace(m[2]); a != "" && t != "" {
			return a, t
		}
	}

	return "", unquote(name)
}

// clean убирает расширение, артефакты yt-dlp и служебные пометки.
func clean(raw string) string {
	name := model.CleanFileName(strings.TrimSpace(raw))
	if ext := filepath.Ext(name); ext != "" && len(ext) <= 5 {
		name = strings.TrimSuffix(name, ext)
	}
	name = noise.ReplaceAllString(name, " ")
	// Подчёркивания в именах файлов заменяют пробелы.
	if !strings.Contains(name, " ") && strings.Contains(name, "_") {
		name = strings.ReplaceAll(name, "_", " ")
	}
	name = spaces.ReplaceAllString(name, " ")
	return strings.Trim(name, " -–—_")
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	for _, pair := range [][2]string{{"«", "»"}, {`"`, `"`}, {"'", "'"}} {
		if strings.HasPrefix(s, pair[0]) && strings.HasSuffix(s, pair[1]) && len(s) > 2 {
			return strings.TrimSpace(s[len(pair[0]) : len(s)-len(pair[1])])
		}
	}
	return s
}

// TrackMeta собирает теги дорожки из названия исходного видео.
func TrackMeta(videoName string) model.AudioMeta {
	artist, title := ParseTitle(videoName)
	return model.AudioMeta{Title: title, Artist: artist}
}
