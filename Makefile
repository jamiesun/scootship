BINARY := scootship
PKG := ./...

.PHONY: build run mock-edge test vet fmt fmt-check docs docs-serve ci clean

## build: compile the single binary into ./bin
build:
	go build -o bin/$(BINARY) ./cmd/scootship

## run: start the center locally in dev mode (dashboard open, demo token seeded)
run: build
	SCOOTSHIP_DEV=1 ./bin/$(BINARY) serve

## mock-edge: run a simulated node against a local center
mock-edge: build
	./bin/$(BINARY) mock-edge -ship-audit -interval 2s

## test: run the test suite
test:
	go test $(PKG)

## vet: run go vet
vet:
	go vet $(PKG)

## fmt: format all Go files
fmt:
	gofmt -w .

## fmt-check: fail if any Go file is not gofmt-clean
fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed in:"; gofmt -l .; exit 1; }

## docs: generate and build the bilingual mdBook site into ./book
docs:
	./scripts/prepare-mdbook.sh
	mdbook build

## docs-serve: generate and preview the bilingual mdBook site locally
docs-serve:
	./scripts/prepare-mdbook.sh
	mdbook serve

## ci: the checks to run before pushing
ci: fmt-check vet test build

## clean: remove build output, docs output, and the local data store
clean:
	rm -rf bin data book .mdbook
