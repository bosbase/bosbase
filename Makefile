lint:
	golangci-lint run -c ./golangci.yml ./...

test:
	CGO_ENABLED=0 go test $(shell go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./...) -v -cover

jstypes:
	go run ./plugins/jsvm/internal/types/types.go

test-report:
	CGO_ENABLED=0 go test $(shell go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./...) -v -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out
