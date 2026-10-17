#!/bin/sh
# Checks the web client without a browser (the CI "web" job):
#   - every JavaScript file parses as an ES module
#   - no external URL in web/, except the AGPL source link in index.html,
#     which is followed, never loaded; vendored files are covered by their
#     checksums instead (scripts/vendor.sh)
#   - no HTML sinks: server text is only shown with x-text or textContent
set -eu
cd "$(dirname "$0")/.."
fail=0

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for f in web/static/*.js; do
  cp "$f" "$tmp/check.mjs"
  if ! node --check "$tmp/check.mjs"; then
    echo "$f: does not parse" >&2
    fail=1
  fi
done

source_link='href="https://github.com/rumblebump/talespeak"'
if grep -rnIE '(https?:)?//[A-Za-z0-9.-]+\.[A-Za-z]{2,}|url\([^)]*(https?:|//)' web --exclude-dir=vendor |
  grep -vF "$source_link"; then
  echo "external URLs in web/ (above): the client must load everything from its own server" >&2
  fail=1
fi

if grep -rnIE 'x-html|innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(|new Function' web --exclude-dir=vendor; then
  echo "HTML sinks in web/ (above): show untrusted text with x-text or textContent" >&2
  fail=1
fi

scripts/vendor.sh check || fail=1
exit $fail
