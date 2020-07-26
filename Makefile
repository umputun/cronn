docker:
	docker build -t umputun/cronn .

dist:
	- @mkdir -p dist
	docker build -f Dockerfile.artifacts -t cronn.bin .
	- @docker rm -f cronn.bin 2>/dev/null || exit 0
	docker run -d --name=cronn.bin cronn.bin
	docker cp cronn.bin:/artifacts dist/
	docker rm -f cronn.bin

race_test:
	cd app && go test -race -mod=vendor -timeout=60s -count 1 ./...

.PHONY: dist docker race_test