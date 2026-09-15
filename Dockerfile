# ---------- builder ----------
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates curl

WORKDIR /src

# templ для кодогенерации шаблонов
RUN go install github.com/a-h/templ/cmd/templ@v0.3.887

# Зависимости (кешируются отдельно)
COPY go.mod go.sum ./
RUN go mod download

# Исходники
COPY . .

# Генерируем Go-код из .templ файлов
RUN templ generate ./web/templates/...

# Собираем бинарь (CGO не нужен — modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/talmor ./cmd/talmor

# ---------- runtime ----------
# Базовый образ держим свежим: 3.20 вышла в мае 2024, ветки поддерживаются
# около двух лет, и обновления безопасности для неё уже не выходят — а внутри
# ffmpeg и python, которые разбирают недоверенные данные из интернета.
FROM alpine:3.22

# python3 нужен для yt-dlp; ffmpeg для конвертации видео
RUN apk --no-cache add ca-certificates ffmpeg tzdata python3 py3-pip && \
    pip3 install --break-system-packages yt-dlp

ENV TZ=Europe/Moscow

# Работаем без прав root: yt-dlp по определению обрабатывает недоверенное
# содержимое из интернета, а ffmpeg исторически богат на уязвимости разбора.
# Побочная выгода — файлы в медиатеке перестают принадлежать root, что мешало
# и Jellyfin, и самому владельцу.
#
# ВНИМАНИЕ при обновлении: каталог данных, созданный прежними версиями,
# принадлежит root, и запись в него станет невозможна. Нужен разовый
# chown -R 1000:1000 на каталоге, смонтированном в /data.
RUN adduser -D -u 1000 -h /app talmor \
    && mkdir -p /data \
    && chown -R talmor:talmor /data /app
VOLUME ["/data"]

COPY --from=builder --chown=talmor:talmor /app/talmor /app/talmor

# yt-dlp установлен pip в /usr/bin/yt-dlp
ENV YT_DLP_BINARY=/usr/bin/yt-dlp \
    YT_DLP_OUTPUT_DIR=/data \
    YT_DLP_OUTPUT_FORMAT=mp4 \
    DB_PATH=/data/talmor.db \
    HTTP_PORT=8080

EXPOSE 8080

# Проверка живости: без неё зависший процесс не перезапускался — контейнер
# остаётся «живым», и restart-политика не срабатывает. Эндпоинт /health
# доступен без авторизации.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${HTTP_PORT}${HEALTH_ENDPOINT:-/health}" >/dev/null || exit 1

USER talmor

ENTRYPOINT ["/app/talmor"]
