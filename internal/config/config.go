package config

import (
	"path/filepath"
	"strings"

	"github.com/jessevdk/go-flags"
)

type Config struct {
	// Database
	DBPath string `long:"db-path" env:"DB_PATH" default:"/data/talmor.db"`

	// HTTP server
	HTTPPort       string `long:"http-port" env:"HTTP_PORT" default:"8080"`
	HTTPHost       string `long:"http-host" env:"HTTP_HOST" default:""`
	BaseURL        string `long:"base-url" env:"BASE_URL"`
	// BasePath — префикс пути, если приложение смонтировано не в корне (напр. /talmor).
	// Ingress передаёт запросы с полным путём; приложение само снимает префикс.
	BasePath       string `long:"base-path" env:"BASE_PATH" default:""`
	HealthEndpoint string `long:"health-endpoint" env:"HEALTH_ENDPOINT" default:"/health"`
	WebToken       string `long:"web-token" env:"WEB_TOKEN"`

	// Telegram bot
	TelegramBotToken   string  `long:"telegram-bot-token" env:"TELEGRAM_BOT_TOKEN" required:"true"`
	TelegramAllowedIDs []int64 `long:"telegram-allowed-ids" env:"TELEGRAM_ALLOWED_IDS" env-delim:";"`
	TelegramProxy      string  `long:"telegram-proxy" env:"TELEGRAM_PROXY"`
	TelegramDebug      bool    `long:"telegram-debug" env:"TELEGRAM_DEBUG"`

	// yt-dlp
	YtDlpBinary       string `long:"yt-dlp-binary" env:"YT_DLP_BINARY" default:"/app/yt-dlp"`
	YtDlpOutputDir    string `long:"yt-dlp-output-dir" env:"YT_DLP_OUTPUT_DIR" default:"/data"`
	YtDlpOutputFormat string `long:"yt-dlp-output-format" env:"YT_DLP_OUTPUT_FORMAT" default:"mp4"`
	YtDlpProxy        string `long:"yt-dlp-proxy" env:"YT_DLP_PROXY"`
	YtDlpTimeout      int    `long:"yt-dlp-timeout" env:"YT_DLP_TIMEOUT" default:"300"`
	YtDlpExtraArgs    string `long:"yt-dlp-extra-args" env:"YT_DLP_EXTRA_ARGS"`
	// Каталог незавершённых загрузок (вне зоны сканирования). Пусто → <output>/.talmor-tmp.
	YtDlpStagingDir   string `long:"yt-dlp-staging-dir" env:"YT_DLP_STAGING_DIR" default:""`

	// Worker pool
	WorkerCount int `long:"worker-count" env:"WORKER_COUNT" default:"2"`

	// Retry backoff
	RetryBackoffBase    int `long:"retry-backoff-base" env:"RETRY_BACKOFF_BASE" default:"30"`
	RetryMaxDuration    int `long:"retry-max-duration" env:"RETRY_MAX_DURATION" default:"86400"`

	// File health check (секунды между проверками)
	FileCheckInterval int `long:"file-check-interval" env:"FILE_CHECK_INTERVAL" default:"300"`

	// Максимум файлов на один запрос (плейлист/канал)
	YtDlpMaxFilesPerRequest int `long:"ytdlp-max-files" env:"YT_DLP_MAX_FILES_PER_REQUEST" default:"100"`

	// Сканирование директории скачивания (секунды между сканами, 0 — выключено)
	DirScanInterval int `long:"dir-scan-interval" env:"DIR_SCAN_INTERVAL" default:"0"`
}

// LinkBase возвращает корень для построения публичных ссылок: BASE_URL + BASE_PATH.
// Пример: BASE_URL=https://media.example.com, BASE_PATH=/talmor → https://media.example.com/talmor
// BASE_URL уже не должен содержать путь — он хранится в BASE_PATH.
func (c *Config) LinkBase() string {
	base := strings.TrimRight(c.BaseURL, "/")
	prefix := strings.TrimRight(c.BasePath, "/")
	return base + prefix
}

// StagingDir — каталог для незавершённых загрузок, вне зоны работы DirScanner.
// По умолчанию — поддиректория OutputDir с точкой в имени: сканер пропускает dot-каталоги,
// а нахождение на том же ФС гарантирует атомарный rename готового файла в OutputDir.
func (c *Config) StagingDir() string {
	if c.YtDlpStagingDir != "" {
		return c.YtDlpStagingDir
	}
	return filepath.Join(c.YtDlpOutputDir, ".talmor-tmp")
}

// ExtraArgsList возвращает YT_DLP_EXTRA_ARGS как слайс строк.
func (c *Config) ExtraArgsList() []string {
	s := strings.TrimSpace(c.YtDlpExtraArgs)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func Load() (*Config, error) {
	var cfg Config
	_, err := flags.Parse(&cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
