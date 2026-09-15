# The builder image must be at least the version go.mod asks for, or the build
# fails at `go mod download` with GOTOOLCHAIN=local.
FROM golang:1.26-alpine AS builder
WORKDIR /app

# Dependencies first: this layer is cached until go.mod or go.sum changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static binary, so the runtime image needs no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o introduce-api ./cmd/server

# The free DB-IP country database, for counting downloads and copies by
# country. It is published monthly; early in a month this month's file may not
# be up yet, so last month's is the fallback. Rebuilt with every deploy.
FROM alpine:3.21 AS geoip
RUN apk --no-cache add curl
RUN set -e; \
    this=$(date -u +%Y-%m); \
    year=${this%-*}; month=${this#*-}; \
    if [ "$month" = "01" ]; then last="$((year - 1))-12"; \
    else last="$year-$(printf %02d $(expr "$month" - 1))"; fi; \
    for m in "$this" "$last"; do \
      if curl -fsSL "https://download.db-ip.com/free/dbip-country-lite-$m.mmdb.gz" -o /tmp/db.gz; then \
        gunzip -c /tmp/db.gz > /dbip-country-lite.mmdb; exit 0; \
      fi; \
    done; \
    echo "no DB-IP country database for $this or $last" >&2; exit 1

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/introduce-api .
COPY --from=geoip /dbip-country-lite.mmdb .

# Postgres and the API start together; the app retries the first connection.
EXPOSE 8080
CMD ["./introduce-api"]
