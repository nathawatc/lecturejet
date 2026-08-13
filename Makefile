BIN := build/lecturejet

.PHONY: all build test test-race vet bench integration clean

all: build

build:
	mkdir -p build
	CGO_ENABLED=1 go build -a -o $(BIN) ./cmd/lecturejet

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/analyze ./internal/timeline

integration: build
	./scripts/integration.sh ./$(BIN)

clean:
	rm -rf build
