#!/usr/bin/env bash

version=v1.0.0

# --- No serviceable parts below ---

# NB: do not call external binaries on fast path

set -eu

case $OSTYPE in
  darwin*)
    cachedir="$HOME/Library/Caches/gr/bin"
    ;;
  linux*)
    cachedir="${XDG_CACHE_HOME:-$HOME/.cache}/gr/bin"
    :;;
  *)
    echo "Unknown OS $OSTYPE" >&2
    exit 255;;
esac

bin="$cachedir/gr-$version"

temp_dir=
cleanup() {
  if [[ -n "$temp_dir" ]]; then
    rm -rf "$temp_dir"
  fi
}

build_gr() {
  # Slow path, can call external binaries

  mkdir -p "$cachedir"
  temp_dir=$(mktemp -d "$cachedir/gr.XXXXXXX")
  trap cleanup EXIT

  GOBIN="$temp_dir" "${GO:-go}" install github.com/onboard-inc/gr@$version
  mv "$temp_dir/gr" "$bin"
  cleanup # Clean temporary files on success, as 'exec' below will prevent running the trap
}

if ! [[ -f "$bin" ]]; then
  build_gr
fi

exec "$bin" "$@"
