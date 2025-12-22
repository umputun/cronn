docker:
	docker build -t umputun/cronn .

race_test:
	cd app && go test -race -timeout=60s -count 1 ./...

prep_site:
	cp -fv README.md site/docs/index.md
	sed -i 's|https://raw.githubusercontent.com/umputun/cronn/master/site/docs/logo.png|logo.png|' site/docs/index.md
	sed -i 's|^.*https://github.com/umputun/cronn/workflows/build/badge.svg.*$$||' site/docs/index.md
	cd site && mkdocs build

# install playwright browsers (run once or after playwright-go version update)
e2e-setup:
	go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium

# run e2e tests headless (default, for CI and quick checks)
e2e:
	go test -v -count=1 -timeout=5m -tags=e2e ./e2e/...

# run e2e tests with visible UI (for debugging and development)
e2e-ui:
	E2E_HEADLESS=false go test -v -count=1 -timeout=10m -tags=e2e ./e2e/...

.PHONY: docker race_test prep_site e2e-setup e2e e2e-ui