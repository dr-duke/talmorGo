# ---------- builder ----------
FROM golang:1.23-alpine AS builder

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
FROM alpine:3.20

# python3 нужен для yt-dlp; ffmpeg для конвертации видео
RUN apk --no-cache add ca-certificates ffmpeg tzdata python3 py3-pip && \
    pip3 install --break-system-packages yt-dlp

ENV TZ=Europe/Moscow

RUN mkdir -p /data
VOLUME ["/data"]

COPY --from=builder /app/talmor /app/talmor

# yt-dlp установлен pip в /usr/bin/yt-dlp
ENV YT_DLP_BINARY=/usr/bin/yt-dlp \
    YT_DLP_OUTPUT_DIR=/data \
    YT_DLP_OUTPUT_FORMAT=mp4 \
    DB_PATH=/data/talmor.db \
    HTTP_PORT=8080

EXPOSE 8080

ENTRYPOINT ["/app/talmor"]
