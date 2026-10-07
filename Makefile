.PHONY: web-build build run demo install check release clean

web-build:
	sh scripts/web-build.sh

build: web-build
	go build -trimpath -o dist/stackharbor ./cmd/stackharbor
	node scripts/clean-dist.mjs prune

run: build
	./dist/stackharbor $(ARGS)

demo: build
	./dist/stackharbor --root examples/harbor-cafe

install: build
	sh scripts/install.sh

check:
	sh scripts/check.sh

release:
	sh scripts/build-release.sh $(VERSION)

clean:
	node scripts/clean-dist.mjs all
