# Release builds strip debug info (~27 MB → ~18 MB on macOS arm64).
LDFLAGS := -s -w
GOFLAGS := -trimpath
BIN := bin

.PHONY: build release preview gpuprof clean

build release:
	@mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN)/raytracer .

preview:
	@mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN)/preview ./cmd/preview

gpuprof:
	@mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN)/gpuprof ./cmd/gpuprof

clean:
	rm -rf $(BIN)
