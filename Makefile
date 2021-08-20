.PHONY: test
test:
	clear
	go test -timeout 30s -count=1 -cover ./...

lint:
	clear
	golangci-lint run ./...

gen:
	clear
	go generate ./...
