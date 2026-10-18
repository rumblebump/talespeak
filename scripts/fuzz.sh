#!/bin/sh
# Runs every Fuzz* target in the module for a short time each.
# Usage: ./scripts/fuzz.sh [duration]   e.g. ./scripts/fuzz.sh 20s
#
# Go's fuzzing engine (1.26) sometimes reports "context deadline exceeded"
# as a failure when -fuzztime runs out: the coordinator can read the timed
# out context before the workers' context is cancelled too. Such a run
# found nothing (no "Failing input written"), so it runs once more; a real
# failure, or the same report twice, still fails.
set -eu
t=${1:-20s}
out=$(mktemp)
trap 'rm -f "$out"' EXIT
go list -f '{{.ImportPath}} {{.Dir}}' ./... | while read -r pkg dir; do
  for f in $(cat "$dir"/*_test.go 2>/dev/null | sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p'); do
    echo "fuzz $pkg $f ($t)"
    if go test -run '^$' -fuzz "^$f\$" -fuzztime "$t" "$pkg" >"$out" 2>&1; then
      cat "$out"
      continue
    fi
    cat "$out"
    if grep -q 'context deadline exceeded' "$out" && ! grep -q 'Failing input written' "$out"; then
      echo "fuzz $pkg $f: the engine timed out at the end without a finding; running once more"
      go test -run '^$' -fuzz "^$f\$" -fuzztime "$t" "$pkg"
    else
      exit 1
    fi
  done
done
