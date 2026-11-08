#!/usr/bin/env bash
set -euo pipefail

root="${1:-.}"
cd "$root"

missing=()
invalid=()
count=0

while IFS= read -r package_dir; do
  count=$((count + 1))
  readme="$package_dir/README.md"
  if [[ ! -f "$readme" ]]; then
    missing+=("$package_dir")
    continue
  fi

  for heading in '^# ' '^## Role$' '^## API / Methods$' '^## Boundaries$' '^## Verification$'; do
    if ! grep -Eq "$heading" "$readme"; then
      invalid+=("$readme missing heading $heading")
    fi
  done
done < <(go list -f '{{.Dir}}' ./... | sort -u)

if (( ${#missing[@]} > 0 )); then
  printf 'Missing package README:\n'
  printf '  %s\n' "${missing[@]}"
fi
if (( ${#invalid[@]} > 0 )); then
  printf 'Invalid package README:\n'
  printf '  %s\n' "${invalid[@]}"
fi
if (( ${#missing[@]} > 0 || ${#invalid[@]} > 0 )); then
  exit 1
fi

printf 'package README check passed for %d packages\n' "$count"
