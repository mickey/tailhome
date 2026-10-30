#!/bin/sh
# Prepare a release: check the tree, run the tests, set the Gemini extension
# version, commit it and create the annotated tag whose message becomes the
# release notes. It never pushes; it prints the push command instead.
#
# Usage: scripts/release.sh 0.8.0
set -eu

die() { echo "release: $*" >&2; exit 1; }

version=${1:-}
version=${version#v}
echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || die "usage: scripts/release.sh X.Y.Z"
tag="v$version"

cd "$(git rev-parse --show-toplevel)"
[ "$(git branch --show-current)" = main ] || die "switch to main first"
[ -z "$(git status --porcelain)" ] || die "commit or stash your changes first"

git fetch --quiet --tags origin
git merge-base --is-ancestor origin/main HEAD || die "main is behind origin/main; pull first"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "$tag already exists"

last=$(git tag --list 'v*' --sort=-v:refname | head -n 1)
if [ -n "$last" ]; then
	newest=$(printf '%s\n%s\n' "${last#v}" "$version" | sort -V | tail -n 1)
	[ "$newest" = "$version" ] || die "$tag is not newer than $last"
fi

command -v jq >/dev/null || die "jq is required"

echo "Running tests..."
go vet ./...
go test -race ./...
python3 -m unittest discover test/tailnet-preview

# Gemini CLI shows this version to users; the release workflow checks it.
template=$(mktemp)
version_file=
trap 'rm -f "$template" "$version_file"' EXIT
version_file=$(mktemp)
jq --arg v "$version" '.version = $v' gemini-extension.json > "$version_file"
mv "$version_file" gemini-extension.json
bumped=
if ! git diff --quiet gemini-extension.json; then
	git commit --quiet -m "Release $tag" gemini-extension.json
	bumped=1
fi

# Leave the branch as it was if no tag gets created.
undo() {
	git tag --delete "$tag" >/dev/null 2>&1 || true
	if [ -n "$bumped" ]; then
		git reset --quiet --hard HEAD~1
		die "$1; version commit undone"
	fi
	die "$1"
}
trap 'undo "interrupted"' INT TERM

# The tag message is "vX.Y.Z", a blank line, then the notes; goreleaser
# publishes the notes. Lines starting with # are dropped.
{
	printf '%s\n\n\n' "$tag"
	echo "# Write the release notes above. Say what users notice, what changed"
	echo "# in configuration, and anything that reverses an earlier promise."
	if [ -n "$last" ]; then
		echo "# Commits since $last:"
		git log --format='#   %s' "$last..HEAD"
	fi
} > "$template"
git -c core.commentChar='#' tag --annotate --edit --file "$template" "$tag" || undo "tag not created"

if [ -z "$(git tag --list --format='%(contents:body)' "$tag" | tr -d '[:space:]')" ]; then
	undo "empty release notes; tag removed"
fi

echo
echo "Created $tag. Check it with: git show $tag"
echo "Publish with: git push --atomic origin main $tag"
