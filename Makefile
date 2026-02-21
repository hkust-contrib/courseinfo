build:
	@go build -o bin/courseinfod

run: build
	@./bin/courseinfod

test:
	go test -v -race ./...

lint:
	go vet ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
