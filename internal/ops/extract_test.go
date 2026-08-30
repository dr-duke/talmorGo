package ops

import "testing"

// Операция извлечения аудио стала пакетной, но в очереди могли остаться записи
// старого формата с единственным item_id — их нужно продолжать понимать.
func TestExtractAudioPayload_Ids(t *testing.T) {
	cases := []struct {
		name    string
		payload extractAudioPayload
		want    []string
	}{
		{
			name:    "пакетный формат",
			payload: extractAudioPayload{ItemIDs: []string{"a", "b"}},
			want:    []string{"a", "b"},
		},
		{
			name:    "старый одиночный формат",
			payload: extractAudioPayload{ItemID: "one"},
			want:    []string{"one"},
		},
		{
			name:    "список важнее одиночного поля",
			payload: extractAudioPayload{ItemID: "one", ItemIDs: []string{"a"}},
			want:    []string{"a"},
		},
		{
			name:    "пусто",
			payload: extractAudioPayload{},
			want:    nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.payload.ids()
			if len(got) != len(c.want) {
				t.Fatalf("получено %v, ожидалось %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] = %q, ожидалось %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// Извлечение аудио вешает на задание тег — облако тегов должно обновляться.
func TestTopicsFor_ExtractAudioIncludesTags(t *testing.T) {
	var hasTags bool
	for _, topic := range topicsFor(KindExtractAudio) {
		if topic == "tags" {
			hasTags = true
		}
	}
	if !hasTags {
		t.Error("после извлечения аудио облако тегов не обновляется")
	}
}
