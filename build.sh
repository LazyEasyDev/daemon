#!/bin/sh
# Copyright 2026 LazyEasyDev Foundation Ltd.

set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
build_dir="$script_dir/build"

rm -rf "$build_dir"
mkdir -p "$build_dir"

build_target() {
	target_os=$1
	target_arch=$2
	output_arch=${3:-$target_arch}
	extension=${4:-}
	output="$build_dir/daemon-$target_os-$output_arch$extension"

	printf 'Compiling %s/%s\n' "$target_os" "$target_arch"
	(
		cd "$script_dir"
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -o "$output" .
	)
}

build_target darwin amd64
build_target darwin arm64
build_target freebsd amd64
build_target freebsd arm64
build_target linux amd64
build_target linux 386
build_target linux arm64
build_target linux arm arm32
build_target windows amd64 amd64 .exe
build_target windows 386 386 .exe
build_target windows arm64 arm64 .exe