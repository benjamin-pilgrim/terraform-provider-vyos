# Terraform Provider VyOS

## Requirements

- [Terraform](https://www.terraform.io/downloads.html) >= 1.17.1
- [Go](https://golang.org/doc/install) >= 1.26.0

## Building The Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

## Using the provider

Fill this in for each provider

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `go generate`.

In order to run the full suite of Acceptance tests, run `make testacc`.

*Note:* Acceptance tests create real resources, and often cost money to run.

```shell
make testacc
```

## Tested versions and CI

Terraform 1.17.1 is the tested CLI baseline; the former 1.0–1.2 acceptance
matrix is retired. Compatibility with those older CLIs is not claimed by this
maintenance series. Go 1.26 is the minimum build toolchain and is exercised in
the regular test job; documentation and release jobs also use Go 1.27.

Acceptance uses a disposable VM from an official VyOS rolling ISO, verified
against its published SHA256. Every run records the resolved dated build for
replay. See [.ci/README.md](.ci/README.md). Only disposable fixtures are used.
