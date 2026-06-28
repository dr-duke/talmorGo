<p align="center">
  <img src="web/static/logo.svg" width="96" height="96" alt="TalmorGo"/>
</p>

<h1 align="center">TalmorGo</h1>

<p align="center">Telegram-бот и веб-интерфейс для скачивания видео через <a href="https://github.com/yt-dlp/yt-dlp">yt-dlp</a> с хранением в библиотеке Jellyfin.</p>

---

## Возможности

- **Telegram-бот** — добавление ссылок в очередь, просмотр статуса, получение постоянной ссылки на скачанный файл
- **Веб-интерфейс** — управление очередью и файлами, встроенный видеоплеер, переименование, теги, предподписанные ссылки
- **Плейлисты и каналы** — yt-dlp разворачивает плейлисты в отдельные задания автоматически
- **Retry с backoff** — неудачные загрузки повторяются до суток, затем переходят в `failed`; ручной сброс из веб-интерфейса
- **Импорт из директории** — DirScanner подхватывает файлы, скачанные вне бота
- **HTTP / SOCKS5 прокси** — для yt-dlp и Telegram-бота независимо

## Стек

| Слой | Технология |
|------|-----------|
| Бэкенд | Go 1.24, SQLite (`modernc.org/sqlite`) |
| Фронтенд | HTMX + [templ](https://github.com/a-h/templ), Plyr.js |
| Скачивание | yt-dlp (внешний бинарь) |
| Деплой | Docker Compose / Kubernetes |

## Быстрый старт (Docker Compose)

```yaml
services:
  talmor:
    image: ghcr.io/dr-duke/talmorgo:latest
    environment:
      TELEGRAM_BOT_TOKEN: "your-token"
      TELEGRAM_ALLOWED_IDS: "123456789"
      BASE_URL: "https://media.example.com"
    volumes:
      - ./data:/data
    ports:
      - "8080:8080"
```

## Конфигурация

Все параметры задаются переменными окружения.

| Переменная | По умолчанию | Описание |
|------------|-------------|----------|
| `TELEGRAM_BOT_TOKEN` | — | Токен бота (обязателен) |
| `TELEGRAM_ALLOWED_IDS` | — | Разрешённые Telegram ID через `;` |
| `TELEGRAM_PROXY` | — | HTTP/SOCKS5 прокси для бота |
| `BASE_URL` | — | Публичный URL сервиса (для ссылок) |
| `BASE_PATH` | — | Префикс пути, если не в корне (`/talmor`) |
| `SITE_NAME` | `TalmorGo` | Название в шапке веб-интерфейса |
| `HTTP_PORT` | `8080` | Порт HTTP-сервера |
| `WEB_TOKEN` | — | Токен для доступа к веб-интерфейсу |
| `DB_PATH` | `/data/talmor.db` | Путь к базе данных |
| `YT_DLP_BINARY` | `/app/yt-dlp` | Путь к бинарю yt-dlp |
| `YT_DLP_OUTPUT_DIR` | `/data` | Директория для скачанных файлов |
| `YT_DLP_OUTPUT_FORMAT` | `mp4` | Формат видео |
| `YT_DLP_PROXY` | — | HTTP/SOCKS5 прокси для yt-dlp |
| `YT_DLP_TIMEOUT` | `300` | Таймаут одной загрузки (сек) |
| `YT_DLP_EXTRA_ARGS` | — | Дополнительные аргументы yt-dlp |
| `YT_DLP_MAX_FILES_PER_REQUEST` | `100` | Макс. файлов из одного плейлиста |
| `YT_DLP_STAGING_DIR` | `<output>/.talmor-tmp` | Временная директория загрузки |
| `WORKER_COUNT` | `2` | Параллельных загрузок |
| `RETRY_BACKOFF_BASE` | `30` | Начальный интервал повтора (сек) |
| `RETRY_MAX_DURATION` | `86400` | Максимальное время повторов (сек) |
| `DIR_SCAN_INTERVAL` | `0` | Интервал сканирования директории (сек, 0 — выключено) |

## Особенности поведения

- **Удаление файла** сохраняет запись в БД; исходная ссылка и название доступны через отдельный эндпоинт `/files/deleted`
- **Скрытие** убирает запись с главного экрана, не удаляя данные; можно восстановить
- **Отмена** доступна для любого задания; отменённые задания можно скрыть
- **Гонка сканер/загрузка** исключена: yt-dlp пишет во временную папку `.talmor-tmp/<jobID>`, перемещение в `OutputDir` атомарное

## Разработка

```bash
# Генерация templ-шаблонов
go run github.com/a-h/templ/cmd/templ@v0.3.887 generate ./...

# Сборка
go build ./...

# Тесты
go test ./...
```
