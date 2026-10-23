#!/bin/sh
# Checks that CODING.md, the code map, matches the tree (the CI "codemap"
# job):
#   - every source file (.go, .js, .lua, .sh, .py; tests, test data,
#     vendored files, node_modules and git-ignored files left out) is
#     named in CODING.md as
#     `path/to/file`; a pack's room scripts are named once per folder, as
#     `content/<pack>/scripts/`
#   - every path CODING.md names in backticks still exists
#   - no file is too long: source files at most 600 lines, test files
#     (*_test.go, *_test.lua, e2e/) at most 800. A file listed in
#     scripts/size-allow.txt ("path lines") may be longer, but not longer
#     than listed; once it is under the limit its line must go.
set -eu
# File names are split on newlines only and never globbed; names with
# other odd characters fail below.
set -f
IFS='
'
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
# or not code. In a git checkout that is what git tracks or would track
# (build output and other ignored files are left out); elsewhere every
# file.
files() {
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git ls-files --cached --others --exclude-standard | while IFS= read -r f; do
      [ -f "$f" ] && [ ! -L "$f" ] && printf '%s\n' "$f"
    done
  else
    find . \( -name .git -o -name node_modules \) -prune -o -type f -print | sed 's#^\./##'
  fi | grep -vE '(^|/)(node_modules|testdata)/|^web/static/vendor/' | sort -u
}

# The paths CODING.md names in backticks, one per line.
named=$(grep -oE '`[A-Za-z0-9_.][A-Za-z0-9_./-]*`' "$map" | tr -d '`' | sort -u)

for f in $(files | grep -vE '^[A-Za-z0-9_./-]+$' || true); do
  echo "$f: use only letters, digits and _ . / - in file names" >&2
  fail=1
done

# tests matches the test files: they need no line here and may be longer.
tests='_test\.(go|lua)$|^e2e/'

for f in $(files | grep -E '\.(go|js|lua|sh|py)$' | grep -vE "$tests"); do
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
# other import paths in the text are no paths; nor are patterns such as
# `protocol/YYYY-MM-DD-slug.md`.
for p in $(printf '%s\n' "$named" | grep -E '^(cmd|internal|web|content|scripts|docs|e2e|deploy|tasks|protocol|\.github|\.claude)/' | grep -vE 'YYYY|NNNN'); do
  if [ ! -e "$p" ]; then
    echo "$p: named in $map but does not exist" >&2
    fail=1
  fi
done

# allowed FILE prints the line count size-allow.txt allows FILE, or nothing.
allowed() {
  [ -f "$allow" ] || return 0
  awk -v f="$1" '$1 == f && $2 ~ /^[0-9]+$/ { print $2; exit }' "$allow"
}

for f in $(files | grep -E '\.(go|js|lua|sh|py)$'); do
  if printf '%s\n' "$f" | grep -qE "$tests"; then
    limit=$max_test
  else
    limit=$max_source
  fi
  # awk also counts a last line without a newline, which wc -l does not.
  n=$(awk 'END { print NR }' "$f")
  case $n in
  '' | *[!0-9]*)
    echo "$f: cannot count its lines" >&2
    fail=1
    continue
    ;;
  esac
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
  for l in $(awk '!/^#/ && NF && (!(NF == 2 && $2 ~ /^[0-9]+$/) || seen[$1]++) { print NR ": " $0 }' "$allow"); do
    echo "$allow:$l: want \"path lines\", one line per path" >&2
    fail=1
  done
  for f in $(awk '!/^#/ && NF { print $1 }' "$allow"); do
    if [ ! -f "$f" ]; then
      echo "$f: in $allow but does not exist" >&2
      fail=1
    fi
  done
fi

exit $fail
