CONTAINER        := zfs-nas-dashboard
SCREENSHOT_IMAGE := zfs-nas-dashboard-screenshot

.PHONY: up down shell test logs build screenshot fmt claude

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
	docker exec $(CONTAINER) go build -buildvcs=false ./cmd/zfs-nas-dashboard

fmt:
	docker build -q -f Dockerfile.dev -t $(CONTAINER)-dev .
	docker run --rm -v "$(PWD):/app" $(CONTAINER)-dev gofmt -w /app/internal /app/cmd
	docker run --rm -v "$(PWD):/app" $(CONTAINER)-dev prettier --write /app/web/

# Run an interactive Claude Code session inside the dev container.
# Runs as the host user (uid/gid) and mounts the host's HOME path through
# unchanged, so files written by the in-container Claude end up owned by
# the host user — no root-owned droppings under ~/.claude or ~/.claude.json.
# The container has no Docker socket and no host filesystem access outside
# the repo and the two mounted config paths.
claude:
	docker build -f Dockerfile.dev -t $(CONTAINER)-dev .
	touch "$(HOME)/.claude.json"
	docker run -it --rm --init \
	  --user "$$(id -u):$$(id -g)" \
	  -e HOME="$(HOME)" \
	  -v "$(PWD):/app" \
	  -v "$(HOME)/.claude:$(HOME)/.claude" \
	  -v "$(HOME)/.claude.json:$(HOME)/.claude.json" \
	  -w /app \
	  $(CONTAINER)-dev claude

screenshot:
	docker build -f Dockerfile.screenshot -t $(SCREENSHOT_IMAGE) .
	mkdir -p docs/screenshots
	docker run --rm --network host \
	  -v "$(PWD)/docs/screenshots:/app/docs/screenshots" \
	  $(SCREENSHOT_IMAGE)
	python3 -c "\
from PIL import Image; \
imgs = [Image.open('docs/screenshots/mobile-{}.png'.format(t)) for t in ('files','zfs','hardware')]; \
gap = 20; w, h = imgs[0].size; \
out = Image.new('RGBA', (w*3 + gap*2, h), (0,0,0,0)); \
[out.paste(im, (i*(w+gap), 0)) for i, im in enumerate(imgs)]; \
out.save('docs/screenshots/mobile-all.png', compress_level=9) \
"
