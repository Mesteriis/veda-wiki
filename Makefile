.PHONY: build test frontend lint extractor-test extractor-real
UV ?= uv
EXTRACTOR := services/knowledge-extractor
frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/webui/dist
	cp -R web/dist internal/webui/dist
build: frontend
	go build -trimpath -o veda ./cmd/veda
lint:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"
	python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py' --include '**/*.go' --include 'web/src/**/*.ts' --include 'web/src/**/*.tsx' --include 'web/src/**/*.css' --include 'services/**/*.py'
	python3 scripts/check_architecture.py
	go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
test: frontend lint
	cd web && npm run typecheck && npm run lint && npm test
	go test -race ./... && go vet ./...
# Knowledge extractor (Python, uv). Unit tests use a fake model and need no network.
extractor-test:
	cd $(EXTRACTOR) && HF_HUB_OFFLINE=1 $(UV) run --locked pytest -m "not real_model"
# Real GLiNER2.5 checks; fetch the checkpoint first with scripts/fetch_knowledge_model.py.
extractor-real:
	cd $(EXTRACTOR) && HF_HUB_OFFLINE=1 $(UV) run --locked pytest -m real_model -s
