.PHONY: build clean deploy-development deploy-staging deploy-production test lint install-linter

build:
	cd collector && env GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -ldflags="-s -w" -o ../bootstrap

clean:
	rm -rf bootstrap && cd collector && go mod tidy

deploy-development: clean build
	STAGE=development DRY_RUN=true serverless deploy --verbose

deploy-staging: clean build
	STAGE=staging DRY_RUN=true serverless deploy --verbose

deploy-production: clean build
	STAGE=production DRY_RUN=false serverless deploy --verbose

test: clean build
	cd collector && go test ./...

lint:
	cd collector && golangci-lint run
