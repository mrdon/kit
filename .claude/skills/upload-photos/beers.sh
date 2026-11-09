#!/usr/bin/env bash
# Copy clear single-beer shots into Photos/Beers/<Beer>/, the per-beer
# product-photo folders people browse. The originals stay in their dated
# sets; Kit's photo sync skips Beers, so the copies are never indexed twice.
#
#   beers.sh "<Beer name>" <drive-file-id> [<drive-file-id> ...]
#
# Beers and the beer's folder are created if missing, and a file already
# there under the same name is skipped, so a re-run is safe. Copies keep the
# original's name.
#
# Needs gws signed in as an account that can write the Photos folder.
# PHOTOS_FOLDER_ID overrides the root.
set -euo pipefail

ROOT="${PHOTOS_FOLDER_ID:-1lP9MBD6nQILxlxrvtT4rDDlnV02Cq6fq}"

if [ $# -lt 2 ]; then
	sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi
beer="$1"
shift

drive_q() {
	gws drive files list --params "$(jq -nc --arg q "$1" \
		'{q:$q, fields:"files(id,name)", pageSize:100, supportsAllDrives:true, includeItemsFromAllDrives:true}')" 2>/dev/null
}

# Escape a value for a Drive query string literal.
q_escape() { printf '%s' "$1" | sed "s/\\\\/\\\\\\\\/g; s/'/\\\\'/g"; }

# folder <parent-id> <name>: the subfolder's id, created if missing.
folder() {
	local id
	id=$(drive_q "'$1' in parents and name = '$(q_escape "$2")' and mimeType = 'application/vnd.google-apps.folder' and trashed = false" |
		jq -r '.files[0].id // empty')
	if [ -z "$id" ]; then
		id=$(gws drive files create \
			--params '{"supportsAllDrives":true,"fields":"id"}' \
			--json "$(jq -nc --arg n "$2" --arg p "$1" \
				'{name:$n, mimeType:"application/vnd.google-apps.folder", parents:[$p]}')" 2>/dev/null |
			jq -r '.id')
		echo "created folder \"$2\" ($id)" >&2
	fi
	printf '%s' "$id"
}

beer_id=$(folder "$(folder "$ROOT" Beers)" "$beer")
existing=$(drive_q "'$beer_id' in parents and trashed = false" | jq -r '.files[].name')

for src in "$@"; do
	name=$(gws drive files get --params "$(jq -nc --arg f "$src" \
		'{fileId:$f, fields:"name", supportsAllDrives:true}')" 2>/dev/null | jq -r '.name // empty')
	[ -n "$name" ] || { echo "no such Drive file: $src" >&2; exit 1; }
	if grep -qxF "$name" <<<"$existing"; then
		echo "skip  $name (already in Beers/$beer)"
		continue
	fi
	id=$(gws drive files copy \
		--params "$(jq -nc --arg f "$src" '{fileId:$f, supportsAllDrives:true, fields:"id"}')" \
		--json "$(jq -nc --arg n "$name" --arg p "$beer_id" '{name:$n, parents:[$p]}')" 2>/dev/null |
		jq -r '.id // empty') || true
	[ -n "$id" ] || { echo "copy failed: $name" >&2; exit 1; }
	echo "copy  $name -> Beers/$beer ($id)"
done
