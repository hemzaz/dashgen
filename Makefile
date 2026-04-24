.PHONY: build test tidy vet fmt fmt-check check clean

BIN := dashgen

build:
	go build -o $(BIN) ./cmd/dashgen

test:
	go test -race ./...

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w -s .

fmt-check:
	@out=$$(gofmt -l -s .); if [ -n "$$out" ]; then echo "gofmt diffs in:"; echo "$$out"; exit 1; fi

check: fmt-check vet build test

clean:
	rm -f $(BIN)
	rm -rf dist/
