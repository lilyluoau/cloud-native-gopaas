#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export PATH="${HOME}/sdk/go/bin:${HOME}/go/bin:/opt/homebrew/bin:${PATH}"

mkdir -p api/gen/resource/v1 api/gen/scheduler/v1

# Generate one file at a time — protoc-gen-micro rejects mixed go_package modules.
for proto in api/proto/resource/v1/resource.proto api/proto/scheduler/v1/scheduler.proto; do
  protoc \
    --proto_path=api/proto \
    --go_out=api/gen --go_opt=module=github.com/gopaas/platform/api/gen \
    --micro_out=paths=source_relative:api/gen \
    "$proto"
done

# Prefer go-micro v3 imports to match the resume/main path.
for f in api/gen/resource/v1/resource.pb.micro.go api/gen/scheduler/v1/scheduler.pb.micro.go; do
  sed -i '' \
    -e 's|go-micro.dev/v4/api|github.com/asim/go-micro/v3/api|g' \
    -e 's|go-micro.dev/v4/client|github.com/asim/go-micro/v3/client|g' \
    -e 's|go-micro.dev/v4/server|github.com/asim/go-micro/v3/server|g' \
    -e 's|go-micro.dev/v4|github.com/asim/go-micro/v3|g' \
    "$f"
done

echo "generated protobuf + go-micro v3 stubs under api/gen"
