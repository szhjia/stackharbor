.PHONY: build run demo install check release

build:
	go build -trimpath -o dist/stackharbor ./cmd/stackharbor

run: build
	./dist/stackharbor $(ARGS)

demo: build
	./dist/stackharbor --root examples/multi-service

install: build
	sh scripts/install.sh

check:
	sh scripts/check.sh

release:
	sh scripts/build-release.sh $(VERSION)
