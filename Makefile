.PHONY: build test check linux
build:
	go build -trimpath -o bin/sprite-tunnel .

test:
	go test -race ./...

check:
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
	go test -race ./...

linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/sprite-tunnel-linux-amd64 .
