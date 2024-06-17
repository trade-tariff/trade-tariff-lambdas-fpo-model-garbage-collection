.PHONY: build clean deploy-development deploy-staging deploy-production test lint install-linter

build:
	cd collector && env GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -ldflags="-s -w" -o bootstrap

clean:
	rm -rf bootstrap

deploy-development: clean build
	STAGE=development serverless deploy --verbose

deploy-staging: clean build
	STAGE=staging serverless deploy --verbose

deploy-production: clean build
	STAGE=production serverless deploy --verbose

test: clean build
	cd collector && go test ./...

lint:
	cd collector && golangci-lint run
