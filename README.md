# gr
[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/onboard-inc/gr)

This tool provides enhanced `go run`-like functionality:
- it propagates the exit code of the tool being run,
- it caches built binaries, so that the second and subsequent runs are nearly instantaneous.

There are some limitations, yet unresolved, to be aware of:
- it works only on packages, not on individual files,
- it does not support pre-modules mode,
- it supports only Linux and macOS.

## Usage

`gr [go build options] <package> [arguments]`.

`gr` supports a subset of `go build` options, specifically those meaningful for `go run`.

`gr` correctly handles `GOOS`, `GOARCH`, `CGO_ENABLED`, and other environment variables
that influence the compilation process.

`gr` reads the `GO` environment variable to locate the `go` binary, and if not found, it
defaults to running it from `PATH`.

## Usage in your project

See `gr-example.bash` for an example trampoline to build and run `gr` in your project.

## Legal

Copyright Onboard, Inc.

Licensed under the [Apache 2.0](LICENSE) license.

Authors:
  * [Misha Gusarov](https://github.com/dottedmag).
