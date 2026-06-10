.PHONY: test run build

test:
	go test ./...

run:
	go run ./cmd/nre-agent

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o nre-agent ./cmd/nre-agent
