docker:
	docker build -t umputun/cronn .

bin:
	docker build -f Dockerfile.artifacts -t cronn.bin .
	- @docker rm -f cronn.bin 2>/dev/null || exit 0
	- @mkdir -p bin
	docker run -d --name=cronn.bin cronn.bin
	docker cp cronn.bin:/artifacts/cronn.linux-amd64.tar.gz bin/cronn.linux-amd64.tar.gz
	docker cp cronn.bin:/artifacts/cronn.linux-386.tar.gz bin/cronn.linux-386.tar.gz
	docker cp cronn.bin:/artifacts/cronn.linux-arm64.tar.gz bin/cronn.linux-arm64.tar.gz
	docker cp cronn.bin:/artifacts/cronn.darwin-amd64.tar.gz bin/cronn.darwin-amd64.tar.gz
	docker cp cronn.bin:/artifacts/cronn.freebsd-amd64.tar.gz bin/cronn.freebsd-amd64.tar.gz
	docker cp cronn.bin:/artifacts/cronn.windows-amd64.zip bin/cronn.windows-amd64.zip
	docker rm -f cronn.bin

race_test:
	cd app && go test -race -mod=vendor -timeout=60s -count 1 ./...

.PHONY: bin docker