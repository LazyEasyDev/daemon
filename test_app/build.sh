#!/bin/sh

set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_dir=$(dirname "$script_dir")
build_dir="$script_dir/build"

rm -rf "$build_dir"
mkdir -p "$build_dir"
printf '%s\n' 'daemon-util relative path test passed' > "$build_dir/relative-path-test.txt"

build_target() {
	target_os=$1
	target_arch=$2
	output_arch=${3:-$target_arch}
	extension=${4:-}
	target_arm=${5:-}
	output="$build_dir/test-app-$target_os-$output_arch$extension"

	printf 'Compiling test app for %s/%s\n' "$target_os" "$target_arch"
	(
		cd "$repo_dir"
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOARM="$target_arm" \
			go build -trimpath -o "$output" ./test_app
	)
}

build_target darwin amd64
build_target darwin arm64
build_target freebsd amd64
build_target freebsd arm64
build_target linux amd64
build_target linux 386
build_target linux arm64
build_target linux arm arm32 "" 6
build_target windows amd64 amd64 .exe
build_target windows 386 386 .exe
build_target windows arm64 arm64 .exe