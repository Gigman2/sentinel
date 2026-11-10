# Sentinel API. On start it migrates the database, reconciles the demo
# tenant (seed) and serves on :8080.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sentinel-api ./cmd/sentinel-api

FROM alpine:3.22
RUN adduser -D -H -u 10001 sentinel
COPY --from=build /out/sentinel-api /usr/local/bin/sentinel-api
USER sentinel
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=2s --timeout=3s --retries=30 CMD ["sentinel-api", "healthcheck"]
ENTRYPOINT ["/bin/sh", "-c", "sentinel-api migrate && sentinel-api seed && exec sentinel-api serve"]
