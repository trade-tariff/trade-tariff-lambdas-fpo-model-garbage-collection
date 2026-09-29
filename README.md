# FPO model garbage collection

This scheduled Go Lambda removes obsolete Fast Parcel Operator (FPO) model
artifacts from S3. It compares model versions with branches in the FPO search
repository and preserves versions marked as deployed to staging or production.

The code is in [collector/](collector/). Deployment and scheduling are configured
in [serverless.yml](serverless.yml).

## Develop and check changes

Use a Go version compatible with [collector/go.mod](collector/go.mod). From the
repository root, run the tests without invoking the Lambda:

```sh
cd collector
go test ./...
```

`make build` builds the Linux Lambda binary. `make lint` requires golangci-lint.
See [the Makefile](Makefile) and [workflows](.github/workflows/) for the build and
check commands.

## Run and deploy safely

Running the handler is an operational action, not a local test. It reads remote
Git branches and S3 objects and can delete model artifacts.

`DRY_RUN` must be set explicitly to `true` or `false`; missing or invalid values
cause an error. The development and staging Make targets set it to `true`.
The production target sets it to `false`. A dry run still requires authorised
access to shared resources.

Branch pushes can deploy to the shared development environment. Check the
workflow and target account before pushing operational changes. Production
deployment and deletion require explicit approval.

## Contribute

Read [CONTRIBUTING.md](CONTRIBUTING.md) for the fork workflow, checks and private
security reporting.

## Licence

The code and associated documentation use the [MIT licence](LICENCE.md), with
Crown copyright (HM Revenue & Customs). Dependencies and model data retain
their own terms.
