#!/bin/sh
# Checks the web client without a browser (the CI "web" job):
#   - every JavaScript file parses as an ES module
#   - no URL with a scheme or a //host anywhere in web/ (IP addresses and
#     localhost included), except the AGPL source link in index.html, which
#     is followed, never loaded; vendored files are covered by their
#     checksums instead (scripts/vendor.sh)
#   - no HTML sinks: server text is only shown with x-text or textContent
#   - no URL bindings or URL assignments other than the picture's src,
#     which view.js validates (clean.js imageURL), and no style bindings
#   - no fetch other than the account API's: the one exact line in
#     api.js, which checks the path; no XMLHttpRequest or sendBeacon, and
#     no WebSocket other than the one exact line in net.js
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

# Copies of the files to scan, with the one allowed link cut out of
# index.html only (the rest of its line is still checked).
mkdir "$tmp/web"
(cd web && find . -path ./static/vendor -prune -o -type f -print) | while read -r f; do
  mkdir -p "$tmp/web/$(dirname "$f")"
  cp "web/$f" "$tmp/web/$f"
done
sed -i 's#href="https://github.com/rumblebump/talespeak"##' "$tmp/web/static/index.html"
# An SVG names its namespace with a URL; nothing loads from it.
[ ! -f "$tmp/web/static/icon.svg" ] || sed -i 's#^<svg xmlns="http://www.w3.org/2000/svg" #<svg #' "$tmp/web/static/icon.svg"

# report MESSAGE: prints the matches read from stdin and fails, if there are any.
report() {
  found=$(sed "s#$tmp/##")
  if [ -n "$found" ]; then
    printf '%s\n' "$found"
    echo "$1" >&2
    fail=1
  fi
}

# Only // comment lines of JavaScript and Go files may name URLs (and hold
# //go:embed); every other line, CSS and markup included, may not.
report "external URLs in web/ (above): the client must load everything from its own server" <<EOF2
$(grep -rnIiE '[a-z][a-z0-9+.-]*://[a-z0-9[]|(^|[^:a-z0-9])//[a-z0-9[]|url\([^)]*//' "$tmp/web" | grep -vE '^[^:]+\.(js|go):[0-9]+:[[:space:]]*//' || true)
EOF2

report "HTML sinks in web/ (above): show untrusted text with x-text or textContent" <<EOF2
$(grep -rnIE 'x-html|innerHTML|outerHTML|insertAdjacentHTML|setHTMLUnsafe|createContextualFragment|DOMParser|srcdoc|document\.write|\beval\(|new Function|setAttribute\(' "$tmp/web" || true)
EOF2

report "URL bindings in web/ (above): only the picture's src is allowed, and view.js validates it" <<EOF2
$(grep -rnIE '(:|x-bind:)(href|src|action|formaction|srcset|data|poster|style)=' "$tmp/web" | sed 's/:src="picture\.url"//g' |
	grep -E '^[^:]+:[0-9]+:.*(:|x-bind:)(href|src|action|formaction|srcset|data|poster|style)=' || true)
EOF2

report "URL assignments in web/ (above): the client never loads or opens a URL from script" <<EOF2
$(grep -rnIE '\.(src|href|srcset|action|formAction|poster|data)[[:space:]]*=[^=]|location[[:space:]]*=[^=]|location\.(href|assign|replace)|window\.open|\.style\.|setProperty\(|new (Image|Audio|Worker|SharedWorker|EventSource|WebSocket)\(|importScripts|import\(|fetch\(|XMLHttpRequest|sendBeacon' "$tmp/web" |
	grep -vE "^$tmp/web/static/api\.js:[0-9]+:    resp = await fetch\(apiPath, \{ \.\.\.init, credentials: \"same-origin\", cache: \"no-store\" \}\);\$" |
	grep -vE "^$tmp/web/static/net\.js:[0-9]+:    const ws = new WebSocket\(scheme \+ location\.host \+ \"/ws\?world=\" \+ world \+ as\);\$" || true)
EOF2

scripts/vendor.sh check || fail=1
exit $fail
