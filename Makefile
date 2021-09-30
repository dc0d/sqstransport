.PHONY: test
test:
	clear
	go test -timeout 30s -count=1 -cover ./...

cover:
	clear
	go test -count=1 -timeout 10s -coverprofile=cover-profile.out -covermode=set -coverpkg=./... ./...; \
	go tool cover -html=cover-profile.out -o cover-coverage.html

lint:
	clear
	golangci-lint run ./...

gen:
	clear
	go generate ./...
