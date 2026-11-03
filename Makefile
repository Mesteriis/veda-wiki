.PHONY: build test frontend lint
frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/webui/dist
	cp -R web/dist internal/webui/dist
build: frontend
	go build -trimpath -o veda ./cmd/veda
lint:
	python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py'
	go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
test: frontend lint
	node --test web/src/*.test.js
	go test -race ./... && go vet ./...
