#!/usr/bin/env bash
# Re-vendor the Patchbay kit into internal/web/static/vendor/patchbay and docs/dev/patchbay, and
# rewrite the pin and per-file hashes in internal/web/static/vendor/VENDOR.md.
#
#   make vendor-patchbay KIT=../patchbay-ui-kit REF=<commit>     # files from a kit commit (git archive)
#   make vendor-patchbay KIT=../patchbay-ui-kit REF=<commit> DIST=<dir>
#                                                                # files from a build output folder instead
#                                                                # (tools/build.sh), for trying a kit branch
#                                                                # before its generated files are committed
#
# Runs on the host (bash, git, tar, sha256sum, awk); it reads nothing from the network.
set -euo pipefail

KIT="${KIT:?KIT=<path to a patchbay-ui-kit clone>}"
REF="${REF:?REF=<kit commit>}"
DIST="${DIST:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/internal/web/static/vendor/patchbay"
DOCS="$ROOT/docs/dev/patchbay"
VENDOR="$ROOT/internal/web/static/vendor/VENDOR.md"
FILES=(patchbay.css patchbay.mjs pb-theme-boot.js tokens.json LICENSE fonts)
# Kit docs copied as they are into docs/dev/patchbay (README.md is merged with snap's own section).
DOCFILES=(README.md CHANGELOG.md COMPONENT-GUIDE.md DESIGN.md)

FULL="$(git -C "$KIT" rev-parse --verify "$REF^{commit}")"
SHORT="${FULL:0:7}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 1. Stage the kit files and docs: from the commit, or from a build folder.
if [ -n "$DIST" ]; then
  SRC="$(cd "$DIST" && pwd)"
  for f in "${FILES[@]}" "${DOCFILES[@]}" components; do cp -r "$SRC/$f" "$TMP/"; done
  FROM="a build of \`$SHORT\` (\`tools/build.sh\` output; the commit's generated files may differ)"
else
  git -C "$KIT" archive "$FULL" "${FILES[@]}" "${DOCFILES[@]}" components | tar -x -C "$TMP"
  FROM="\`git archive $SHORT ${FILES[*]}\`"
fi

# 2. Vendored files, byte for byte.
rm -rf "$DEST" && mkdir -p "$DEST"
for f in "${FILES[@]}"; do cp -r "$TMP/$f" "$DEST/"; done

# 3. Docs: the kit README with snap's "Using these files" section kept after its title, the changelog,
#    the component guide and design guide, and README.md + example.html per component.
USING="$TMP/using.md"
awk '/^## Using these files/{on=1} on{print} /^The rest of this file is the kit.s own README\./{exit}' "$DOCS/README.md" > "$USING"
[ -s "$USING" ] || { echo "vendor-patchbay: no \"## Using these files\" section in $DOCS/README.md" >&2; exit 1; }
sed -i -E "s/\(\`[0-9a-f]{7,40}\` on \`main\`/(\`$SHORT\` on \`main\`/" "$USING"
rm -rf "$DOCS" && mkdir -p "$DOCS/components"
{ head -n 2 "$TMP/README.md"; cat "$USING"; echo; tail -n +3 "$TMP/README.md"; } > "$DOCS/README.md"
for f in CHANGELOG.md COMPONENT-GUIDE.md DESIGN.md; do cp "$TMP/$f" "$DOCS/$f"; done
for d in "$TMP"/components/*/; do
  n="$(basename "$d")"; mkdir -p "$DOCS/components/$n"
  for f in README.md example.html; do [ -f "$d$f" ] && cp "$d$f" "$DOCS/components/$n/"; done
done
# snap-share's public history replaced the owner's name in these docs; keep it that way (LICENSE keeps its copyright line).
grep -rlIi 'casey' "$DOCS" | while read -r f; do sed -i -e 's/CASEY/RILEY/g; s/Casey/Riley/g; s/casey/riley/g' "$f"; done
if grep -rIilE --exclude=LICENSE 'casey|cweed|seaweed' "$DOCS" "$DEST" >/dev/null; then
  echo "vendor-patchbay: a personal name or host is left in:" >&2; grep -rIilE --exclude=LICENSE 'casey|cweed|seaweed' "$DOCS" "$DEST" >&2; exit 1
fi

# 4. VENDOR.md: the pin in the patchbay row, the source line, and one sha256 row per vendored file.
ROWS="$TMP/rows.md"
( cd "$DEST/.." && for f in "${FILES[@]}"; do find "patchbay/$f" -type f | LC_ALL=C sort; done | while read -r p; do
    printf '| `%s` | `%s` |\n' "$p" "$(sha256sum "$p" | cut -d' ' -f1)"; done ) > "$ROWS"
awk -v full="$FULL" -v from="$FROM" -v rows="$ROWS" '
  /^\| `patchbay\/` \|/ { sub(/commit `[0-9a-f]{40}`[^|]*\|/, "commit `" full "` |") }
  /^Copied byte for byte from/ { print "Copied byte for byte from the commit above (" from "). Update a file and its line here together, or rerun `make vendor-patchbay`."; next }
  /^\| `patchbay\/[^`]+` \| `[0-9a-f]{64}` \|$/ { if (!done) { while ((getline l < rows) > 0) print l; done=1 } next }
  { print }
' "$VENDOR" > "$TMP/VENDOR.md"
mv "$TMP/VENDOR.md" "$VENDOR"
echo "Vendored patchbay $SHORT ($(wc -l < "$ROWS") files) and its docs ($(ls "$DOCS/components" | wc -l) components)."
