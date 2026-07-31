FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o introduce-api ./cmd/server

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/introduce-api .
EXPOSE 8080
CMD ["./introduce-api"]
