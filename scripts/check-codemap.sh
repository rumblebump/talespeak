#!/bin/sh
# Checks that CODING.md, the code map, matches the tree (the CI "codemap"
# job):
#   - every source file (.go, .js, .lua, .sh, .py; tests, test data,
#     vendored files and node_modules left out) is named in CODING.md as
#     `path/to/file`; a pack's room scripts are named once per folder, as
#     `content/<pack>/scripts/`
#   - every path CODING.md names in backticks still exists
#   - no file is too long: source files at most 600 lines, test files
#     (*_test.go, e2e/) at most 800. A file listed in
#     scripts/size-allow.txt ("path lines") may be longer, but not longer
#     than listed; once it is under the limit its line must go.
set -eu
cd "$(dirname "$0")/.."
fail=0
map=CODING.md
allow=scripts/size-allow.txt
max_source=600
max_test=800

if [ ! -f "$map" ]; then
  echo "$map is missing" >&2
  exit 1
fi

# files lists every file of the tree, without the parts that are not ours
# or not code.
files() {
  find . \( -name .git -o -name node_modules -o -name vendor -o -name testdata \) -prune -o -type f -print |
    sed 's#^\./##' | sort
}

# The paths CODING.md names in backticks, one per line.
named=$(grep -oE '`[A-Za-z0-9_.][A-Za-z0-9_./-]*`' "$map" | tr -d '`' | sort -u)

for f in $(files | grep -E '\.(go|js|lua|sh|py)$' | grep -vE '_test\.go$|^e2e/'); do
  case $f in
  content/*/scripts/*.lua) want=$(dirname "$f")/ ;;
  *) want=$f ;;
  esac
  if ! printf '%s\n' "$named" | grep -qxF "$want"; then
    echo "$want: not in $map; add a line saying what it does" >&2
    fail=1
  fi
done

# Only paths under the repo's own folders are checked, so `log/slog` and
# other import paths in the text are no paths.
for p in $(printf '%s\n' "$named" | grep -E '^(cmd|internal|web|content|scripts|docs|e2e|deploy|tasks|protocol|\.github|\.claude)/'); do
  if [ ! -e "$p" ]; then
    echo "$p: named in $map but does not exist" >&2
    fail=1
  fi
done

# allowed FILE prints the line count size-allow.txt allows FILE, or nothing.
allowed() {
  [ -f "$allow" ] || return 0
  awk -v f="$1" '$1 == f { print $2 }' "$allow"
}

for f in $(files | grep -E '\.(go|js|lua)$'); do
  case $f in
  *_test.go | e2e/*) limit=$max_test ;;
  *) limit=$max_source ;;
  esac
  n=$(wc -l <"$f" | tr -d ' ')
  extra=$(allowed "$f")
  if [ -n "$extra" ]; then
    if [ "$n" -le "$limit" ]; then
      echo "$f: $n lines, at most $limit: remove its line from $allow" >&2
      fail=1
    elif [ "$n" -gt "$extra" ]; then
      echo "$f: $n lines, $allow allows $extra: split it by function instead of growing it" >&2
      fail=1
    fi
  elif [ "$n" -gt "$limit" ]; then
    echo "$f: $n lines, at most $limit: split it by function (CODING.md, Style guide)" >&2
    fail=1
  fi
done

if [ -f "$allow" ]; then
  for f in $(awk '!/^#/ && NF { print $1 }' "$allow"); do
    if [ ! -f "$f" ]; then
      echo "$f: in $allow but does not exist" >&2
      fail=1
    fi
  done
fi

exit $fail
