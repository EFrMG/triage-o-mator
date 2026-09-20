#!/usr/bin/env sh
# Build triage-o-mator, then install it into a repository you want to triage:
#
#   ./install.sh /path/to/repository [options]
#
# Anything after the path goes to bin/install-to (--solo, --repo owner/repo, --dry-run, --yes, ...); run that script directly to install again without rebuilding.
set -e

if [ $# -eq 0 ]; then
	echo "usage: ./install.sh /path/to/repository [bin/install-to options]" >&2
	exit 2
fi

target=$1
shift

cd "$(dirname "$0")"

if command -v mise >/dev/null 2>&1; then
	mise install
	mise exec -- make build
else
	echo "warning: mise not found, building with the Go on PATH; mise.toml has the pinned toolchain." >&2
	make build
fi

exec ./bin/install-to "$target" "$@"
