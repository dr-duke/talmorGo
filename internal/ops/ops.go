// Package ops описывает виды фоновых пакетных операций, их видимость в очереди
// и класс исполнения.
package ops

const (
	KindBulkTag      = "bulk_tag"
	KindBulkHide     = "bulk_hide"
	KindBulkMeta     = "bulk_meta"
	KindExtractAudio = "extract_audio"
	KindUpdateMeta   = "update_meta"
	KindReindex      = "reindex"
	KindCleanup      = "cleanup"
)

// ExtractedAudioTag — тег, которым помечается задание после извлечения дорожки:
// по нему в медиатеке одним кликом видно, из чего аудио уже доставали.
const ExtractedAudioTag = "аудио"

// Класс операции определяет полосу исполнения. Лёгкие операции — только запись
// в БД, они завершаются мгновенно; тяжёлые запускают ffmpeg или обходят весь
// диск. Полосы независимы, поэтому «Извлечь аудио» больше не ждёт, пока
// проставятся теги на две сотни файлов, и наоборот.
const (
	ClassLight = "light"
	ClassHeavy = "heavy"
)

// Class сопоставляет вид операции с полосой исполнения.
var Class = map[string]string{
	KindBulkTag:      ClassLight,
	KindBulkHide:     ClassLight,
	KindBulkMeta:     ClassHeavy,
	KindExtractAudio: ClassHeavy,
	KindUpdateMeta:   ClassHeavy,
	KindReindex:      ClassHeavy,
	KindCleanup:      ClassHeavy,
}

// ShowInQueue управляет тем, отображается ли каждый вид операций в UI очереди.
// Присвойте false, чтобы скрыть конкретный тип — операции будут по-прежнему выполняться.
var ShowInQueue = map[string]bool{
	KindBulkTag:      true,
	KindBulkHide:     true,
	KindBulkMeta:     true,
	KindExtractAudio: true,
	KindUpdateMeta:   true,
	KindReindex:      false, // системные операции — не отображаем в очереди
	KindCleanup:      false,
}

// VisibleKinds возвращает виды операций, включённые для отображения в очереди.
func VisibleKinds() []string {
	out := make([]string, 0, len(ShowInQueue))
	for k, visible := range ShowInQueue {
		if visible {
			out = append(out, k)
		}
	}
	return out
}

// KindsOfClass возвращает виды операций указанного класса.
func KindsOfClass(class string) []string {
	out := make([]string, 0, len(Class))
	for kind, c := range Class {
		if c == class {
			out = append(out, kind)
		}
	}
	return out
}
