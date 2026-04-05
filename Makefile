CONTAINER := nas-dashboard

.PHONY: up down shell test logs build

up:
	docker compose up -d

down:
	docker compose down

shell:
	docker exec -it $(CONTAINER) bash

test:
	docker exec -t $(CONTAINER) go test -buildvcs=false -v ./...

logs:
	docker compose logs -f

build:
	docker exec $(CONTAINER) go build -buildvcs=false ./cmd/nas-dashboard
