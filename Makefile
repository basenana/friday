export GO111MODULE=on

LDFLAGS = -s -w

friday:
	go version
	go build -ldflags="$(LDFLAGS)"  -o bin/friday cmd/main.go

friday.amd:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)"  -o bin/friday-amd cmd/main.go

friday.arm:
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)"  -o bin/friday-arm cmd/main.go

test:
	@go test ./...
