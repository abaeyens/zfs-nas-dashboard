CONTAINER        := zfs-nas-dashboard
SCREENSHOT_IMAGE := zfs-nas-dashboard-screenshot

.PHONY: up down shell test logs build screenshot

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

screenshot:
	docker build -f Dockerfile.screenshot -t $(SCREENSHOT_IMAGE) .
	mkdir -p docs/screenshots
	docker run --rm --network host \
	  -v "$(PWD)/docs/screenshots:/app/docs/screenshots" \
	  $(SCREENSHOT_IMAGE)
