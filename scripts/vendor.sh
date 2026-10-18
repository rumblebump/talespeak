#!/bin/sh
# Third-party browser code is vendored: copied into web/static/vendor/ and
# pinned in web/vendor.json by version and SHA-256, never loaded from a CDN.
#
#   scripts/vendor.sh check                 the files match their checksums (offline)
#   scripts/vendor.sh verify                each file is exactly its package from npm
#   scripts/vendor.sh update NAME VERSION   download, copy and pin a new version
#
# npm checks the package against the registry's integrity hash when it
# downloads it. Renovate bumps "version" in web/vendor.json; verify then fails
# until someone runs update on the branch and reviews the diff.
set -eu
cd "$(dirname "$0")/.."
manifest=web/vendor.json

sha() { sha256sum "$1" | cut -d' ' -f1; }

# fetch NAME VERSION DIR: unpacks the package into DIR and prints the file
# path. Only a registry package at an exact version: a manifest that names a
# git URL or a range is refused, and no package script ever runs.
fetch() {
  pkg=$(jq -r --arg n "$1" '.[$n].package' "$manifest")
  file=$(jq -r --arg n "$1" '.[$n].file' "$manifest")
  if ! printf '%s' "$pkg" | grep -Eqx '(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*'; then
    echo "web/vendor.json: package $pkg is not an npm package name" >&2
    exit 1
  fi
  if ! printf '%s' "$2" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+'; then
    echo "version $2 is not an exact version like 3.17.4" >&2
    exit 1
  fi
  if ! printf '%s' "$file" | grep -Eqx 'package/[A-Za-z0-9._/-]+' || printf '%s' "$file" | grep -q '\.\.'; then
    echo "web/vendor.json: file $file must be inside the package" >&2
    exit 1
  fi
  (cd "$3" && npm pack --silent --ignore-scripts "$pkg@$2" > /dev/null && tar xzf ./*.tgz)
  echo "$3/$file"
}

# dest NAME: the vendored file's path, which must be in web/static/vendor/.
dest() {
  d=$(jq -r --arg n "$1" '.[$n].dest' "$manifest")
  if ! printf '%s' "$d" | grep -Eqx 'web/static/vendor/[A-Za-z0-9._-]+' || printf '%s' "$d" | grep -q '\.\.'; then
    echo "web/vendor.json: dest $d must be a file in web/static/vendor/" >&2
    exit 1
  fi
  echo "$d"
}

cmd=${1:-check}
case "$cmd" in
check)
  for n in $(jq -r 'keys[]' "$manifest"); do
    dest=$(dest "$n")
    want=$(jq -r --arg n "$n" '.[$n].sha256' "$manifest")
    got=$(sha "$dest")
    if [ "$got" != "$want" ]; then
      echo "$dest: sha256 $got, web/vendor.json says $want" >&2
      exit 1
    fi
    echo "ok $n $(jq -r --arg n "$n" '.[$n].version' "$manifest") $dest"
  done
  ;;
verify)
  for n in $(jq -r 'keys[]' "$manifest"); do
    tmp=$(mktemp -d)
    src=$(fetch "$n" "$(jq -r --arg n "$n" '.[$n].version' "$manifest")" "$tmp")
    dest=$(dest "$n")
    if [ "$(sha "$src")" != "$(sha "$dest")" ]; then
      echo "$dest is not $n $(jq -r --arg n "$n" '.[$n].version' "$manifest") from npm: run scripts/vendor.sh update $n <version>" >&2
      rm -rf "$tmp"
      exit 1
    fi
    rm -rf "$tmp"
    echo "verified $n"
  done
  "$0" check
  ;;
update)
  n=${2:?name}
  v=${3:?version}
  tmp=$(mktemp -d)
  src=$(fetch "$n" "$v" "$tmp")
  dest=$(dest "$n")
  cp "$src" "$dest"
  jq --arg n "$n" --arg v "$v" --arg s "$(sha "$dest")" '.[$n].version = $v | .[$n].sha256 = $s' "$manifest" > "$tmp/manifest.json"
  mv "$tmp/manifest.json" "$manifest"
  rm -rf "$tmp"
  echo "updated $n to $v; review the diff of $dest"
  ;;
*)
  echo "usage: $0 check | verify | update NAME VERSION" >&2
  exit 2
  ;;
esac
