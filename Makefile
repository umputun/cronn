docker:
	docker build -t umputun/cronn .

race_test:
	cd app && go test -race -timeout=60s -count 1 ./...

prep_site:
	# prepare docs source directory for mkdocs
	rm -rf site/docs-src && mkdir -p site/docs-src
	cp -fv README.md site/docs-src/index.md
	cp -rv site/docs/logo.png site/docs/icon.png site/docs/favicon.png site/docs-src/
	cp -rv site/docs/screenshots site/docs-src/
	sed -i.bak 's|https://raw.githubusercontent.com/umputun/cronn/master/site/docs/logo.png|logo.png|' site/docs-src/index.md && rm -f site/docs-src/index.md.bak
	sed -i.bak 's|^.*https://github.com/umputun/cronn/workflows/build/badge.svg.*$$||' site/docs-src/index.md && rm -f site/docs-src/index.md.bak
	sed -i.bak 's|^.*coveralls.io.*$$||' site/docs-src/index.md && rm -f site/docs-src/index.md.bak
	mkdir -p site/docs-src/stylesheets && cp -fv site/docs/stylesheets/extra.css site/docs-src/stylesheets/
	# build site structure: landing page + docs subdirectory
	rm -rf site/site && mkdir -p site/site
	cp -fv site/docs/index.html site/site/
	cp -fv site/docs/favicon.png site/docs/icon.png site/docs/logo.png site/site/
	cp -rv site/docs/screenshots site/site/
	# build mkdocs into site/site/docs/
	cd site && (test -d .venv || python3 -m venv .venv) && . .venv/bin/activate && pip install -r requirements.txt && mkdocs build

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