build:
	@go build -o bin/courseinfod

run: build
	@./bin/courseinfod

