.PHONY: build test frontend lint
frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/webui/dist
	cp -R web/dist internal/webui/dist
build: frontend
	go build -trimpath -o veda ./cmd/veda
lint:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"
	python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py' --include '**/*.go'
	python3 scripts/check_architecture.py
	go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
test: frontend lint
	cd web && npm run typecheck && npm run lint && npm test
	go test -race ./... && go vet ./...
