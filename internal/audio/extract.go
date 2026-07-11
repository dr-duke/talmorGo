package audio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dr-duke/talmorGo/internal/model"
)

// Extract извлекает аудиодорожку из videoPath, сохраняет как .m4a в outputDir.
// Записывает ID3-метаданные из meta (пустые поля пропускаются).
// Сначала пробует скопировать поток (-c:a copy), при ошибке — перекодирует в AAC.
// Возвращает путь к созданному файлу.
func Extract(ctx context.Context, ffmpegBin, videoPath, outputDir string, meta model.AudioMeta) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create audio dir: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	outPath := filepath.Join(outputDir, base+".m4a")

	if err := run(ctx, ffmpegBin, videoPath, outPath, "copy", meta); err != nil {
		if err2 := run(ctx, ffmpegBin, videoPath, outPath, "aac", meta); err2 != nil {
			return "", fmt.Errorf("ffmpeg copy: %w; ffmpeg aac: %w", err, err2)
		}
	}
	return outPath, nil
}

func run(ctx context.Context, ffmpegBin, input, output, audioCodec string, meta model.AudioMeta) error {
	args := []string{"-i", input, "-vn", "-c:a", audioCodec}
	if meta.Title != "" {
		args = append(args, "-metadata", "title="+meta.Title)
	}
	if meta.Artist != "" {
		args = append(args, "-metadata", "artist="+meta.Artist)
	}
	if meta.Album != "" {
		args = append(args, "-metadata", "album="+meta.Album)
	}
	if meta.Year != "" {
		args = append(args, "-metadata", "date="+meta.Year)
	}
	if meta.Genre != "" {
		args = append(args, "-metadata", "genre="+meta.Genre)
	}
	args = append(args, "-y", output)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncate(string(out), 300))
	}
	return nil
}

// WriteTags перезаписывает ID3-метаданные в существующем аудиофайле.
// Обновляются только поля, присутствующие в fields (title/artist/album/year/genre).
// Файл перезаписывается через временный — перекодирования нет, только ремукс.
func WriteTags(ctx context.Context, ffmpegBin, path string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	ext := filepath.Ext(path)
	tmp := strings.TrimSuffix(path, ext) + ".tagtmp" + ext

	args := []string{"-i", path, "-c", "copy"}
	for _, f := range []string{"title", "artist", "album", "year", "genre"} {
		if val, ok := fields[f]; ok {
			key := f
			if f == "year" {
				key = "date"
			}
			args = append(args, "-metadata", key+"="+val)
		}
	}
	args = append(args, "-y", tmp)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg write tags: %w: %s", err, truncate(string(out), 300))
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace file after tag write: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
