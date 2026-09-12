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

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/introduce-api .

# Postgres and the API start together; the app retries the first connection.
EXPOSE 8080
CMD ["./introduce-api"]
