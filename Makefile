export GO111MODULE=on

LDFLAGS = -s -w

build:
	mkdir -p bin/darwin/arm
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)"  -o bin/darwin/arm/friday cmd/main.go
	mkdir -p bin/darwin/amd
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)"  -o bin/darwin/amd/friday cmd/main.go

	mkdir -p bin/linux/arm
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)"  -o bin/linux/arm/friday cmd/main.go
	mkdir -p bin/linux/amd
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)"  -o bin/linux/amd/friday cmd/main.go


test:
	@go test ./...
