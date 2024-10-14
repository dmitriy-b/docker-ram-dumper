include .env

generate:
	@go generate ./...

build: generate ## Compile the binary
	@rm -rf bin/*
	@mkdir -p bin
	@go build -o bin/$(APP_NAME) cmd/$(APP_NAME)/main.go

build-linux: generate ## Compile the binary for linux
	@env GOOS=linux go build -o bin/$(APP_NAME) cmd/$(APP_NAME)/main.go

build-docker: build-linux ## Build docker image
	@rm ./dumps/*.dmp || true
	@docker build -t $(APP_NAME) .

build-docker-test: build-docker ## Build docker image
	@docker build . -f test/integration/Dockerfile -t ram-dumper-test-image:latest

install: build ## compile the binary and copy it to PATH
	@cp build/* /usr/local/bin

run: build ## Compile and run the binary
	@./bin/$(APP_NAME)

gomod_tidy: ## Run go mod tidy to clean up & install dependencies
	@go mod tidy

format: ## Run gofumpt against code to format it
	@gofumpt -l -w cmd/
	@gofumpt -l -w internal/
	@gofumpt -l -w test/

staticcheck: ## Run staticcheck against code
	@staticcheck ./...

test: generate build-docker-test ## Run tests
	@go clean -testcache 
	@go test ./...

codecov-test: generate ## Run tests with coverage
	@mkdir -p coverage
	@courtney -o coverage/coverage.out ./...
	@go tool cover -html=coverage/coverage.out -o coverage/coverage.html

install-deps: install-gofumpt install-mockgen install-courtney install-staticcheck ## Install dependencies

install-gofumpt: ## Install gofumpt for formatting
	go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)

install-mockgen: ## Install mockgen for generating mocks
	go install github.com/golang/mock/mockgen@$(MOCKGEN_VERSION)

install-courtney: ## Install courtney for code coverage
	go install github.com/dave/courtney@$(COURTNEY_VERSION)

install-staticcheck:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'