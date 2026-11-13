GO ?= go
BIN := bin/guided-pr
ADDR ?= 127.0.0.1:8787

.PHONY: build test run start stop status

build:
	mkdir -p bin
	$(GO) build -ldflags "-X main.version=dev" -o $(BIN) ./cmd/guided-pr

test:
	$(GO) test ./...

run: build
	./$(BIN) serve --addr $(ADDR)

start: build
	./$(BIN) start --addr $(ADDR)

stop:
	./$(BIN) stop --addr $(ADDR)

status:
	./$(BIN) status --addr $(ADDR)
