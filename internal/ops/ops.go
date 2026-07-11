// Package ops описывает виды фоновых пакетных операций и управляет их видимостью в очереди.
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
