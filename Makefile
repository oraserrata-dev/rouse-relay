VERSION ?= 1.0.0
BINARY  := rouse-relay
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all clean docker release

# --- Cross-compiled raw binaries ----------------------------------------

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

# --- Docker image -------------------------------------------------------

docker:
	docker build -t oraserrata/rouse-relay:latest .

# --- GitHub Releases zips -----------------------------------------------
#
# Each zip contains the platform's native binary plus the matching
# install script and (where applicable) the service definition file.
# Lays out files at the zip root so the install scripts can find their
# siblings via $(dirname "$0").
#
# Targets the staging directory `build/stage/` to assemble each zip,
# and emits the finished archives under `build/release/`.

release: all
	@mkdir -p build/release
	$(MAKE) zip-macOS-arm64
	$(MAKE) zip-macOS-amd64
	$(MAKE) zip-linux-amd64
	$(MAKE) zip-linux-arm64
	$(MAKE) zip-windows-amd64
	@echo ""
	@echo "Release zips written to build/release/:"
	@ls -lh build/release/

zip-macOS-arm64: build/$(BINARY)-darwin-arm64
	@rm -rf build/stage && mkdir -p build/stage build/release
	cp $< build/stage/$(BINARY)
	cp install-macos.sh com.oraserrata.rouse-relay.plist build/stage/
	cd build/stage && zip -r ../release/RouseRelay-macOS-arm64.zip .
	@rm -rf build/stage

zip-macOS-amd64: build/$(BINARY)-darwin-amd64
	@rm -rf build/stage && mkdir -p build/stage build/release
	cp $< build/stage/$(BINARY)
	cp install-macos.sh com.oraserrata.rouse-relay.plist build/stage/
	cd build/stage && zip -r ../release/RouseRelay-macOS-amd64.zip .
	@rm -rf build/stage

zip-linux-amd64: build/$(BINARY)-linux-amd64
	@rm -rf build/stage && mkdir -p build/stage build/release
	cp $< build/stage/$(BINARY)
	cp install-linux.sh rouse-relay.service build/stage/
	cd build/stage && zip -r ../release/RouseRelay-linux-amd64.zip .
	@rm -rf build/stage

zip-linux-arm64: build/$(BINARY)-linux-arm64
	@rm -rf build/stage && mkdir -p build/stage build/release
	cp $< build/stage/$(BINARY)
	cp install-linux.sh rouse-relay.service build/stage/
	cd build/stage && zip -r ../release/RouseRelay-linux-arm64.zip .
	@rm -rf build/stage

zip-windows-amd64: build/$(BINARY)-windows-amd64.exe
	@rm -rf build/stage && mkdir -p build/stage build/release
	cp $< build/stage/$(BINARY).exe
	cp install-windows.bat build/stage/
	cd build/stage && zip -r ../release/RouseRelay-windows-amd64.zip .
	@rm -rf build/stage

clean:
	rm -rf build/
