CONTAINER := nas-dashboard

.PHONY: up down shell test logs build

up:
	docker compose up -d

down:
	docker compose down

shell:
	docker exec -it $(CONTAINER) bash

test:
	docker exec $(CONTAINER) go test ./...

logs:
	docker compose logs -f

build:
	docker exec $(CONTAINER) go build -buildvcs=false ./cmd/nas-dashboard
