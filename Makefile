SHELL := /bin/bash
VERSION ?= 1.0.3
ARCHIVE_EXECUTION ?= required
COMMIT ?= $(shell if git rev-parse --is-inside-work-tree >/dev/null 2>&1 && test -z "$$(git status --porcelain --untracked-files=normal)"; then git rev-parse HEAD; else echo local-uncommitted; fi)
BUILT_AT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.builtAt=$(BUILT_AT)
GOFILES := $(shell find . -name '*.go' -type f -not -path './dist/*' -not -path './bin/*' -not -path './local/*' -not -path './.visual-brainstorming/*')

.PHONY: all verify fmt fmt-check vet test race coverage docs-check api-check workflow-check license-check scripts-check build smoke http-smoke web-install web-build web-unit web-test web-static-check web-verify web-e2e live-smoke docker dist package package-only release-verify clean

all: verify build

verify: fmt-check vet test race docs-check api-check workflow-check license-check scripts-check smoke

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@files="$$(gofmt -l $(GOFILES))"; \
	if [[ -n "$$files" ]]; then \
		echo "The following Go files need gofmt:" >&2; \
		echo "$$files" >&2; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

coverage:
	mkdir -p .tmp
	go test -covermode=atomic -coverprofile=.tmp/coverage.out ./...
	go tool cover -func=.tmp/coverage.out
	python3 scripts/check-coverage.py .tmp/coverage.out --total 65 --core 75

docs-check:
	python3 scripts/check-docs.py

api-check:
	ruby scripts/check-openapi.rb

workflow-check:
	ruby scripts/check-workflows.rb
	# v1.7.8+ requires Go 1.24; v1.7.7 is the latest release compatible with go.mod's Go 1.23 baseline.
	# ShellCheck runs separately above because actionlint v1.7.7 can deadlock while feeding large run blocks on Darwin.
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck '' -config-file .github/actionlint.yaml .github/workflows/*.yml

license-check:
	python3 scripts/check-third-party-licenses.py

scripts-check:
	bash -n scripts/*.sh
	python3 -c 'import ast, pathlib; [ast.parse(p.read_text(encoding="utf-8"), filename=str(p)) for p in pathlib.Path("scripts").glob("*.py")]'
	python3 -m unittest discover -s scripts -p 'test_*.py'
	@for script in scripts/*.rb; do ruby -c "$$script" >/dev/null; done
	ruby scripts/test_check_workflows.rb

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/sfs ./cmd/sfs

smoke:
	./scripts/smoke.sh

http-smoke:
	./scripts/http-smoke.sh

web-install:
	npm --prefix web ci --no-audit --no-fund

web-build:
	npm --prefix web run build

web-unit:
	npm --prefix web run test

# Build web/dist without syncing it first, then compare it with the committed
# Go embed tree. This keeps static drift visible in clean local and CI runs.
web-static-check:
	npm --prefix web run typecheck
	npm --prefix web run build:web
	npm --prefix web run check:static

web-verify: web-unit web-static-check

web-e2e:
	npm --prefix web run test:e2e

web-test: web-verify web-e2e

live-smoke: build
	python3 scripts/live-smoke.py bin/sfs

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILT_AT=$(BUILT_AT) -t superfeishusearch:$(VERSION) .
	docker run --rm superfeishusearch:$(VERSION) version

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-linux-amd64 ./cmd/sfs
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-linux-arm64 ./cmd/sfs
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-darwin-amd64 ./cmd/sfs
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-darwin-arm64 ./cmd/sfs
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-windows-amd64.exe ./cmd/sfs
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/sfs-windows-arm64.exe ./cmd/sfs

package: verify http-smoke web-install web-test package-only

package-only:
	COMMIT=$(COMMIT) ./scripts/package-release.sh $(VERSION)

release-verify:
	python3 scripts/verify-release.py --dir dist/release --version $(VERSION) --commit $(COMMIT) --archive-execution $(ARCHIVE_EXECUTION)

clean:
	rm -rf bin dist .tmp
