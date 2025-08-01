# Этап сборки (builder)
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /talmorGo
RUN mkdir -p /app/bin/ && \
    wget -O /app/bin/yt-dlp https://github.com/yt-dlp/yt-dlp/releases/download/2025.07.21/yt-dlp_linux

COPY pkg/* ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/talmor-go

FROM alpine:latest AS runtime
RUN apk --no-cache add ca-certificates

ENV TELEGRAM_BOT_TOKEN=""
ENV YT_DLP_PROXY=""
ENV YT_DLP_BINARY=/app/yt-dlp

COPY --from=builder /app/bin/* /app/

RUN chmod +x $YT_DLP_BINARY
LABEL app.version=0.0.1
LABEL app.author=glebpyanov
ENTRYPOINT ["/app/talmor-go"]