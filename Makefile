CONTAINER        := zfs-nas-dashboard
SCREENSHOT_IMAGE := zfs-nas-dashboard-screenshot

.PHONY: up down shell test logs build screenshot fmt

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

fmt:
	docker exec $(CONTAINER) gofmt -w /app/internal /app/cmd
	docker exec $(CONTAINER) prettier --write /app/web/

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
