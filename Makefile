lint: check-english
	golangci-lint run -c ./golangci.yml ./...

# documentation and code comments must be written in English (see CLAUDE.md)
check-english:
	go run ./scripts/check-english

test:
	CGO_ENABLED=0 go test $(shell go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./...) -v -cover

jstypes:
	go run ./plugins/jsvm/internal/types/types.go

test-report:
	CGO_ENABLED=0 go test $(shell go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./...) -v -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out
