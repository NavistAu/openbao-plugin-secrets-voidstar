#!/bin/sh
# Tag-gated release: build linux amd64+arm64 tarballs + SHA256SUMS and
# publish them as a Forgejo release. Runs inside the CI golang image.
# Env: CI_COMMIT_TAG (Woodpecker), FORGEJO_TOKEN (repo secret).
# Lives as a script because Woodpecker substitutes ${...} inside YAML
# commands at config time (pipeline 2's mangled artifacts) and its
# parser rejects $$-escapes it can't read (pipeline 4's parse error).
set -eu

VERSION="${CI_COMMIT_TAG#v}"
BIN=openbao-plugin-secrets-voidstar
API=https://git.29c.sh/api/v1/repos/29c/openbao-plugin-secrets-voidstar

for GOARCH in amd64 arm64; do
  mkdir -p "dist/linux_$GOARCH"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -o "dist/linux_$GOARCH/$BIN" "./cmd/$BIN"
  tar -C "dist/linux_$GOARCH" -czf "${BIN}_${VERSION}_linux_${GOARCH}.tar.gz" "$BIN"
done
sha256sum "${BIN}"_"${VERSION}"_linux_*.tar.gz > SHA256SUMS
cat SHA256SUMS

# Create-or-lookup: a rerun (pipeline restart, retag) finds the
# release already present — 409 is not an error, fetch its id.
CREATE=$(curl -sS -w '\n%{http_code}' -X POST "$API/releases" \
  -H "Authorization: token $FORGEJO_TOKEN" -H "Content-Type: application/json" \
  -d "{\"tag_name\":\"$CI_COMMIT_TAG\",\"name\":\"$CI_COMMIT_TAG\"}")
CODE=$(printf '%s' "$CREATE" | tail -1)
case "$CODE" in
  201) BODY=$(printf '%s' "$CREATE" | sed '$d') ;;
  409) BODY=$(curl -sSf "$API/releases/tags/$CI_COMMIT_TAG" \
         -H "Authorization: token $FORGEJO_TOKEN") ;;
  *) echo "release create returned $CODE" >&2; exit 1 ;;
esac
# grep -o emits matches in order; the release object's own id is the
# first "id" field in the document (a greedy sed here once matched the
# author's id instead — pipeline 11).
RID=$(printf '%s' "$BODY" | grep -o '"id":[0-9]*' | head -1 | tr -dc 0-9)
[ -n "$RID" ] || { echo "no release id resolved" >&2; exit 1; }

for A in "${BIN}_${VERSION}_linux_amd64.tar.gz" "${BIN}_${VERSION}_linux_arm64.tar.gz" SHA256SUMS; do
  curl -sSf -X POST "$API/releases/$RID/assets?name=$A" \
    -H "Authorization: token $FORGEJO_TOKEN" -F "attachment=@$A" -o /dev/null
  echo "uploaded $A"
done
