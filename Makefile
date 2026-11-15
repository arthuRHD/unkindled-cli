BINARY := unkindled
GO ?= go

.PHONY: build test fmt vet clean

build:
	$(GO) build -trimpath -o bin/$(BINARY) .

test:
	$(GO) test ./...

fmt:
	gofmt -w .

vet:
	$(GO) vet ./...

clean:
	rm -rf bin
