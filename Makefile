.PHONY: run build docker-build docker-push deploy

IMAGE := ghcr.io/steven230500/introduce-api

run:
	go run ./cmd/server

build:
	CGO_ENABLED=0 go build -o introduce-api ./cmd/server

docker-build:
	docker build -t $(IMAGE):latest .

docker-push: docker-build
	docker push $(IMAGE):latest

# Run on droplet: ssh root@159.203.110.122 "cd /opt/introduce && docker compose pull && docker compose up -d"
deploy:
	ssh root@159.203.110.122 "cd /opt/introduce && docker compose pull && docker compose up -d"
