package model

import "testing"

func TestCleanFileName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"хвост машет котом [CRbLJq6Pgew].mp4", "хвост машет котом.mp4"},
		{"ФЕНOMЕН THE LAST OF US [Nx9euQ9zU90].mp4", "ФЕНOMЕН THE LAST OF US.mp4"},
		{"ФЕНOMЕН MORROWIND [OJg1WGppJEs].mp4", "ФЕНOMЕН MORROWIND.mp4"},
		{"Are You Satisfied? .f140.m4a", "Are You Satisfied?.m4a"},
		{"Zoltraak - Frieren Beyond Journey's End.f140.m4a", "Zoltraak - Frieren Beyond Journey's End.m4a"},
		{"Юр.mp4", "Юр.mp4"},
		{"Восток.m4a", "Восток.m4a"},
		{"normal_video.mp4", "normal_video.mp4"},
		{"video [abc].mp4", "video [abc].mp4"},         // ID too short — не трогаем
		{"video [toolongidthatismorethan15chars].mp4", "video [toolongidthatismorethan15chars].mp4"}, // слишком длинный
	}
	for _, c := range cases {
		got := cleanFileName(c.in)
		if got != c.want {
			t.Errorf("cleanFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
