.PHONY: all build web go clean dev

all: build

build: web go

web:
	cd web && npm install && npm run build
	rm -rf cmd/et/dist
	cp -r web/dist cmd/et/dist

go:
	go build -o et ./cmd/et

clean:
	rm -rf web/dist cmd/et/dist web/node_modules et

dev:
	cd web && npm run dev &
	go run ./cmd/et server
