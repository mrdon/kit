#!/usr/bin/env bash
# Upload exported photos into the Drive folder Kit's poster photo library
# syncs from, one subfolder per set.
#
#   upload.sh "<subfolder name>" <src>:<NewName.jpg> [<src>:<NewName.jpg> ...]
#
# Each source (PNG, TIFF or JPEG) is converted to a full-size JPEG under the
# new name, so the filename can carry what is in the shot (Kit's indexer reads
# it). The subfolder is created under the Photos root if missing, and a file
# already in it under the same name is skipped, so a re-run is safe.
#
# Needs gws (Google Workspace CLI) signed in as an account that can write the
# Photos folder. PHOTOS_FOLDER_ID overrides the root.
set -euo pipefail

ROOT="${PHOTOS_FOLDER_ID:-1lP9MBD6nQILxlxrvtT4rDDlnV02Cq6fq}"
QUALITY="${JPEG_QUALITY:-90}"

if [ $# -lt 2 ]; then
	sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi
folder_name="$1"
shift

drive_q() {
	gws drive files list --params "$(jq -nc --arg q "$1" \
		'{q:$q, fields:"files(id,name)", pageSize:100, supportsAllDrives:true, includeItemsFromAllDrives:true}')" 2>/dev/null
}

# Escape a value for a Drive query string literal.
q_escape() { printf '%s' "$1" | sed "s/\\\\/\\\\\\\\/g; s/'/\\\\'/g"; }

folder_id=$(drive_q "'$ROOT' in parents and name = '$(q_escape "$folder_name")' and mimeType = 'application/vnd.google-apps.folder' and trashed = false" |
	jq -r '.files[0].id // empty')
if [ -z "$folder_id" ]; then
	folder_id=$(gws drive files create \
		--params '{"supportsAllDrives":true,"fields":"id"}' \
		--json "$(jq -nc --arg n "$folder_name" --arg p "$ROOT" \
			'{name:$n, mimeType:"application/vnd.google-apps.folder", parents:[$p]}')" 2>/dev/null |
		jq -r '.id')
	echo "created folder \"$folder_name\" ($folder_id)"
fi

existing=$(drive_q "'$folder_id' in parents and trashed = false" | jq -r '.files[].name')

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for pair in "$@"; do
	src="${pair%%:*}"
	name="${pair#*:}"
	if [ "$src" = "$pair" ] || [ -z "$name" ]; then
		echo "bad argument \"$pair\": want <src>:<NewName.jpg>" >&2
		exit 2
	fi
	[ -f "$src" ] || { echo "no such file: $src" >&2; exit 1; }
	case "$name" in *.jpg | *.jpeg) ;; *) name="$name.jpg" ;; esac
	if grep -qxF "$name" <<<"$existing"; then
		echo "skip  $name (already in \"$folder_name\")"
		continue
	fi
	out="$tmp/$name"
	sips -s format jpeg -s formatOptions "$QUALITY" "$src" --out "$out" >/dev/null
	# gws refuses an --upload path outside the current directory.
	id=$(cd "$tmp" && gws drive files create \
		--params '{"supportsAllDrives":true,"fields":"id"}' \
		--json "$(jq -nc --arg n "$name" --arg p "$folder_id" '{name:$n, parents:[$p]}')" \
		--upload "./$name" --upload-content-type image/jpeg | jq -r '.id // empty') || true
	[ -n "$id" ] && [ "$id" != null ] || { echo "upload failed: $name" >&2; exit 1; }
	echo "up    $name  ($(du -h "$out" | cut -f1 | tr -d ' '), $id)"
done
