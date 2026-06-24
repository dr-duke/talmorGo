package config

import (
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

	// Worker pool
	WorkerCount int `long:"worker-count" env:"WORKER_COUNT" default:"2"`

	// Retry backoff
	RetryBackoffBase    int `long:"retry-backoff-base" env:"RETRY_BACKOFF_BASE" default:"30"`
	RetryMaxDuration    int `long:"retry-max-duration" env:"RETRY_MAX_DURATION" default:"86400"`

	// File health check (секунды между проверками)
	FileCheckInterval int `long:"file-check-interval" env:"FILE_CHECK_INTERVAL" default:"300"`
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
