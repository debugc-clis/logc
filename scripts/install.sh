#!/usr/bin/env sh
set -eu

VERSION=${VERSION:-dev}
PREFIX=${PREFIX:-/usr/local}

echo "building logc ${VERSION}"
go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o logc .
install -d "${PREFIX}/bin"
install -m 755 logc "${PREFIX}/bin/logc"
echo "installed ${PREFIX}/bin/logc"
