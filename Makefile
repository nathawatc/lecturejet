BIN := build/lecturejet
C_SOURCES := native/lecturejet.c $(wildcard native/*/*.c)

.PHONY: all build native-check test test-race vet bench integration clean

all: build

build:
	mkdir -p build
	CGO_ENABLED=1 go build -o $(BIN) ./cmd/lecturejet

native-check:
	@for f in $(C_SOURCES); do \
		gcc -Wall -Wextra -Werror -pedantic -std=gnu11 -fsyntax-only $$f $$(pkg-config --cflags libavformat libavcodec libavutil libswresample libavfilter) || exit 1; \
	done

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
