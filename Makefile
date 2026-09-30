.PHONY: generate schema-update test build check release-check

generate:
	go generate ./...
schema-update:
	go run ./cmd/update-schema
	go generate ./...
test:
	go test -race ./...
build:
	go build -trimpath -o bin/tailctl ./cmd/tailctl
check:
	go vet ./...
	go test -race ./...
release-check:
	goreleaser check
	goreleaser release --snapshot --clean
