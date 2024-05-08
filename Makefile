docker:
	docker build -t umputun/cronn .

dist:
	- @mkdir -p dist
	docker build -f Dockerfile.artifacts -t cronn.bin --progress=plain .
	- @docker rm -f cronn.bin 2>/dev/null || exit 0
	docker run -d --name=cronn.bin cronn.bin
	docker cp cronn.bin:/artifacts dist/
	docker rm -f cronn.bin

race_test:
	cd app && go test -race -timeout=60s -count 1 ./...

prep_site:
	cp -fv README.md site/docs/index.md
	sed -i 's|https://raw.githubusercontent.com/umputun/cronn/master/site/docs/logo.png|logo.png|' site/docs/index.md
	sed -i 's|^.*https://github.com/umputun/cronn/workflows/build/badge.svg.*$$||' site/docs/index.md
	cd site && mkdocs build


.PHONY: dist docker race_test prep_site