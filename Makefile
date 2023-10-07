export GO111MODULE=on

LDFLAGS = -s -w

friday:
	go version
	go build -ldflags="$(LDFLAGS)"  -o bin/friday cmd/main.go

friday.linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)"  -o bin/friday-linux cmd/main.go

test:
	@go test ./...
