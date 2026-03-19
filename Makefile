VERSION ?= 1.0.0
BINARY  := rouse-relay
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all clean docker

all: build/$(BINARY)-darwin-arm64 \
     build/$(BINARY)-darwin-amd64 \
     build/$(BINARY)-linux-amd64 \
     build/$(BINARY)-linux-arm64 \
     build/$(BINARY)-windows-amd64.exe

build/$(BINARY)-darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $@ .

build/$(BINARY)-darwin-amd64:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $@ .

build/$(BINARY)-linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $@ .

build/$(BINARY)-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $@ .

build/$(BINARY)-windows-amd64.exe:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $@ .

docker:
	docker build -t oraserrata/rouse-relay:latest .

clean:
	rm -rf build/
