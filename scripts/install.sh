#!/usr/bin/env sh
set -eu

VERSION=${VERSION:-dev}
PREFIX=${PREFIX:-/usr/local}

echo "building logg ${VERSION}"
go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o logg .
install -d "${PREFIX}/bin"
install -m 755 logg "${PREFIX}/bin/logg"
echo "installed ${PREFIX}/bin/logg"
