test:
	go test -short ./...

test_all:
	golangci-lint run
	go test -race ./...
