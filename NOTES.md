# Design notes

## Caching

`gr` stores cached executables in the [user's cache directory](https://pkg.go.dev/os#UserCacheDir):
`~/Library/Caches/gr` on macOS, `~/.cache/gr` on Linux, unless overridden.

For every package, `gr` keeps at most two previous versions of the executable file in the cache.

The caching key is derived from the source code:
- find and parse `go.mod` to understand what's located where, considering both `import` and `replace` directives,
- read all the local source code and follow the imports,
- create a checksum using the contents of source files, `go.mod` and `go.sum` files, and compilation options.

## Parsing

Parsing is done lazily to make this tool usable in monorepos.

Locating source code is hand-rolled for significant speed improvement over calling `go list`:
- match `*.go`, `*.S` and CGo files,
- ignore non-regular files,
- ignore `*_test.go`, `.*` and `_*`.
