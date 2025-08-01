# Этап сборки (builder)
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /talmorGo
RUN wget https://github.com/yt-dlp/yt-dlp/releases/download/2025.07.21/yt-dlp_linux

COPY pkg/* ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/myapp

FROM alpine:latest AS runtime
RUN apk --no-cache add ca-certificates

ENV TELEGRAM_BOT_TOKEN=""
ENV YT_DLP_PROXY=""
ENV YT_DLP_BINARY=/usr/local/bin/yt-dlp

COPY --from=builder /pkg/yt-dlp_linux $YT_DLP_BINARY
COPY --from=builder /app/bin/myapp /myapp
RUN chmod +x $YT_DLP_BINARY
LABEL app.version=0.0.1
LABEL app.author=glebpyanov
ENTRYPOINT ["/myapp"]