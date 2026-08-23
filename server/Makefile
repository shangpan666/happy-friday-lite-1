SHELL := /bin/sh

ZVEC_ROOT := $(CURDIR)/native/zvec
GO := go

export CGO_ENABLED := 1
export CGO_CFLAGS := -I$(ZVEC_ROOT)/include
export CGO_LDFLAGS := -L$(ZVEC_ROOT)/darwin_arm64 -lzvec_c_api -Wl,-rpath,$(ZVEC_ROOT)/darwin_arm64

.PHONY: test build run

test:
	$(GO) test ./...

build:
	$(GO) build -o happy-friday-server .

run:
	$(GO) run .
