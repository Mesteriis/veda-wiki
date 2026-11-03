.PHONY: build test frontend
frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf cmd/veda/ui
	cp -R web/dist cmd/veda/ui
build: frontend
	go build -trimpath -o veda ./cmd/veda
test: frontend
	node --test web/src/*.test.js
	go test -race ./... && go vet ./...
