build:
	@go build -o bin/acch

run: build
	@./bin/acch

