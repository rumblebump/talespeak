#!/bin/sh
# Runs every Fuzz* target in the module for a short time each.
# Usage: ./scripts/fuzz.sh [duration]   e.g. ./scripts/fuzz.sh 20s
set -eu
t=${1:-20s}
go list -f '{{.ImportPath}} {{.Dir}}' ./... | while read -r pkg dir; do
  for f in $(cat "$dir"/*_test.go 2>/dev/null | sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p'); do
    echo "fuzz $pkg $f ($t)"
    go test -run '^$' -fuzz "^$f\$" -fuzztime "$t" "$pkg"
  done
done
