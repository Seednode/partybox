#!/usr/bin/env bash

package_name="partybox"
mkdir -p builds

platforms=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/386"
  "linux/amd64"
  "linux/arm"
  "linux/arm64"
  "windows/386"
  "windows/amd64"
  "windows/arm64"
)

for platform in "${platforms[@]}"; do
  IFS=" " read -r -a platform_split <<< "${platform//\// }"
  GOOS="${platform_split[0]}"
  GOARCH="${platform_split[1]}"
  output_name="${package_name}-${GOOS}-${GOARCH}"
  ld_flags='-s -w'
  env GOOS="${GOOS}" GOARCH="${GOARCH}" CC="musl-gcc" CGO_ENABLED=0 go build -trimpath -ldflags "${ld_flags}" -tags "netgo timetzdata" -o "builds/${output_name}"
done
