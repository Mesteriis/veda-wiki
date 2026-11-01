.PHONY: build test frontend
frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf server/ui
	cp -R web/dist server/ui
build: frontend
	cd server && go build -trimpath -o ../veda .
test: frontend
	cd server && go test -race ./... && go vet ./...
