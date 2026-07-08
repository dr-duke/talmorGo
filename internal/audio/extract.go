package audio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Extract извлекает аудиодорожку из videoPath и сохраняет её как .m4a в outputDir.
// Сначала пробует скопировать поток (-c:a copy), при ошибке — перекодирует в AAC.
// Возвращает путь к созданному файлу.
func Extract(ctx context.Context, ffmpegBin, videoPath, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create audio dir: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	outPath := filepath.Join(outputDir, base+".m4a")

	if err := run(ctx, ffmpegBin, videoPath, outPath, "copy"); err != nil {
		// Fallback: перекодируем в AAC если поток несовместим с m4a
		if err2 := run(ctx, ffmpegBin, videoPath, outPath, "aac"); err2 != nil {
			return "", fmt.Errorf("ffmpeg copy: %w; ffmpeg aac: %w", err, err2)
		}
	}
	return outPath, nil
}

func run(ctx context.Context, ffmpegBin, input, output, audioCodec string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-i", input,
		"-vn",
		"-c:a", audioCodec,
		"-y",
		output,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncate(string(out), 300))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
