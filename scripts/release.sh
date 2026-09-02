#!/usr/bin/env bash
#
# Cut a release: one tag, two commits, one push.
#
#   scripts/release.sh 0.2.0        (normally: make release VERSION=0.2.0)
#
# The version is MANDATORY and is never inferred. `master` carries
# `x.y.z-SNAPSHOT`, so a number is always available to default to — and
# defaulting to it would be wrong, because that number was chosen when the last
# release closed, before anyone knew what the batch would contain. Only someone
# reading the commits since the last tag knows whether they are a patch or a
# minor. Run this with no argument and it prints those commits and the
# candidates, then exits without doing anything.
#
# What it does, in order, so the failure modes are readable:
#
#   1. refuses unless master is clean, current and untagged for this version,
#      and CHANGELOG.md has something under Unreleased to release
#   2. writes the version into the constant, the docs and the changelog, runs
#      `make check` and `make dist` — and puts the tree back if either fails,
#      so a failed release leaves no trace
#   3. commits the release, tags it, commits the next -SNAPSHOT
#   4. pushes all three refs with --atomic
#
# Step 4 is one push on purpose. A branch push that lands without its tag leaves
# `master` claiming a release that was never published, and the recovery is
# fiddly; --atomic makes that state unreachable. It also means a merge that
# landed while `make check` was running fails the whole thing cleanly, and you
# re-run rather than untangle.

set -uo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || {
	echo "error: not in a git checkout" >&2
	exit 1
}

MAIN_GO=cmd/relay-cli/main.go
CHANGELOG=CHANGELOG.md
BRANCH=master

die() {
	echo >&2
	echo "error: $*" >&2
	exit 1
}

step() { printf '\n%s\n' "$1"; }
note() { printf '  %s\n' "$1"; }

# The constant, exactly as the Makefile and the release workflow read it.
constant() { make version; }

# semver_gt A B — true when A is strictly greater, comparing numerically so
# 0.10.0 beats 0.9.0. Both are bare x.y.z; -SNAPSHOT is stripped by the caller.
semver_gt() {
	local a="$1" b="$2"
	[ "$a" = "$b" ] && return 1
	[ "$(printf '%s\n%s\n' "$a" "$b" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" = "$b" ]
}

last_tag() { git describe --tags --abbrev=0 2>/dev/null || true; }

# The entries under `## Unreleased` in the changelog, blank lines dropped. Empty
# when the section is missing or has nothing in it.
unreleased() {
	awk '/^## Unreleased$/ { in_section = 1; next } /^## / { in_section = 0 } in_section' "$CHANGELOG" |
		grep -v '^[[:space:]]*$' || true
}

# What someone needs in order to choose the number: the range, and what it
# contains. This is the whole reason the argument is mandatory rather than
# defaulted — the choice is a reading of these lines.
recommend() {
	local current base tag range major minor patch
	current=$(constant)
	base=${current%-SNAPSHOT}
	tag=$(last_tag)

	echo "error: make release needs a version. It is never guessed."
	echo
	if [ -n "$tag" ]; then
		range="$tag..HEAD"
		note "last release   $tag"
	else
		range="HEAD"
		note "last release   none yet — this would be the first"
	fi
	note "master says    $current"
	echo
	note "commits since:"
	git log --oneline "$range" | sed 's/^/    /'
	echo

	IFS=. read -r major minor patch <<<"$base"
	note "a fix, or docs only   make release VERSION=$major.$minor.$patch"
	note "anything new          make release VERSION=$major.$((minor + 1)).0"
	note "breaking              (0.x — say so in the release notes; there is"
	note "                       no 1.0 until the interface settles)"
	echo
	note "The number on master ($base) is the default only if the batch above is"
	note "what it was chosen for. Read it before deciding."
	exit 2
}

[ $# -ge 1 ] && [ -n "${1:-}" ] || recommend
VERSION="$1"

# ── 1. refuse early ──────────────────────────────────────────────────────────
step "checking"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
	die "version must be bare x.y.z — no leading v, no -SNAPSHOT. Got: $VERSION"

case "$VERSION" in
0.*) ;;
*) die "everything here is 0.x until the interface settles, and a test enforces it.
       If leaving 0.x is deliberate, delete that test in its own commit first." ;;
esac

CURRENT=$(constant)
[ -n "$CURRENT" ] || die "could not read the version constant from $MAIN_GO"

case "$CURRENT" in
*-SNAPSHOT) ;;
*) die "the constant is $CURRENT, with no -SNAPSHOT. master should never sit on a
       released version: either a release is half-finished (check git log and
       git tag), or someone edited the constant by hand." ;;
esac

BASE=${CURRENT%-SNAPSHOT}
if semver_gt "$BASE" "$VERSION"; then
	die "$VERSION is lower than $BASE, which master already claims as its next
       release. A version never goes backwards — a published number means one
       tree forever."
fi

TAG="v$VERSION"
LAST=$(last_tag)
if [ -n "$LAST" ]; then
	if ! semver_gt "$VERSION" "${LAST#v}"; then
		die "$TAG is not greater than the last release $LAST."
	fi
fi

git rev-parse --verify --quiet "refs/tags/$TAG" >/dev/null &&
	die "$TAG already exists locally. A tag that never published is re-runnable with:
       gh workflow run release.yml -f tag=$TAG"

# The changelog is what a user reads to learn what changed, and a test holds
# the docs' version to a heading in it — so the release writes that heading
# from the Unreleased section, and there has to be something in it to write.
grep -q '^## Unreleased$' "$CHANGELOG" 2>/dev/null ||
	die "$CHANGELOG has no '## Unreleased' section. The release moves its entries
       under '## $TAG'; add the section back, with what changed, and re-run."

[ -n "$(unreleased)" ] ||
	die "$CHANGELOG has nothing under Unreleased. A release says what changed, so
       add at least one line there — for a fix or docs-only batch, say that —
       commit it, and re-run."

[ -z "$(git status --porcelain)" ] ||
	die "the working tree is dirty. A release commit should contain the version
       bump and nothing else."

[ "$(git rev-parse --abbrev-ref HEAD)" = "$BRANCH" ] ||
	die "releases are cut from $BRANCH, and you are on $(git rev-parse --abbrev-ref HEAD)."

note "fetching origin"
git fetch --quiet --tags origin || die "git fetch failed"

git ls-remote --tags origin "$TAG" | grep -q . &&
	die "$TAG already exists on origin. Publish a new version rather than moving a
       tag someone may already have downloaded."

if ! git merge-base --is-ancestor "origin/$BRANCH" HEAD; then
	die "$BRANCH is behind origin/$BRANCH. Pull first — a release is cut from what
       everyone else has."
fi

note "ok — releasing $TAG from $(git rev-parse --short HEAD)"

# ── 2. write the version, then prove the tree ────────────────────────────────
#
# The checks run AFTER the bump because that is the tree being released: the
# documentation tests compare the docs against the constant, so bumping first is
# what makes "the docs match this release" a thing the suite can answer.

# set_version <x.y.z[-SNAPSHOT]> — the constant, plus the sample `relay x.y.z`
# lines in the user docs and the changelog heading, which a test holds to it.
set_version() {
	local v="$1"
	perl -pi -e "s/^(\t*version = \")[^\"]*(\")/\${1}$v\${2}/" "$MAIN_GO"
	# Docs show what a released binary prints, so they carry the release number
	# with no -SNAPSHOT — and are left alone when starting the next one.
	case "$v" in
	*-SNAPSHOT) return 0 ;;
	esac
	local f
	for f in readme.md docs/*.md; do
		perl -pi -e "s/\brelay \d+\.\d+\.\d+/relay $v/g" "$f"
	done
	# The Unreleased entries become this release's, under a fresh Unreleased
	# heading for the next batch. The next -SNAPSHOT leaves it alone: that empty
	# section is already the place its changes go.
	perl -0pi -e "s/^## Unreleased\n/## Unreleased\n\n## v$v\n/m" "$CHANGELOG"
}

# Only the files set_version writes. Narrow on purpose: this runs on a tree the
# script already proved was clean, and a blanket `git checkout -- .` in a script
# anyone might run from a dirty checkout is a footgun waiting for the one time
# the clean check is moved.
restore() {
	git checkout -- "$MAIN_GO" "$CHANGELOG" readme.md docs 2>/dev/null
}

set_version "$VERSION"

step "make check"
if ! make check; then
	restore
	die "the tree does not pass its own checks, so it is not going out.
       Nothing was committed and the version was put back.

       If this was a documentation test, that is the check doing its job: the
       docs describe a version this release would contradict."
fi

step "make dist"
if ! make dist >/dev/null; then
	restore
	die "the release build failed. Nothing was committed and the version was put back."
fi
note "ok"

# ── 3. confirm ───────────────────────────────────────────────────────────────
step "about to release"
note "tag        $TAG"
note "commit     $(git rev-parse --short HEAD) on $BRANCH"
note "artifacts  $(ls dist/ | tr '\n' ' ')"
echo
note "changelog, now under ## $TAG:"
awk -v tag="$TAG" '$0 == "## " tag { in_section = 1; next } /^## / { in_section = 0 } in_section' "$CHANGELOG" |
	grep -v '^[[:space:]]*$' | sed 's/^/    /'
echo
git --no-pager diff --stat | sed 's/^/  /'
echo

if [ ! -t 0 ]; then
	restore
	die "not a terminal. A release is confirmed by a person, so this refuses to
       run unattended."
fi

printf '  tag and push %s? [y/N] ' "$TAG"
read -r reply
case "$reply" in
y | Y | yes | YES) ;;
*)
	restore
	echo "  nothing done."
	exit 1
	;;
esac

# ── 4. two commits, one tag, one push ────────────────────────────────────────
step "committing"

git commit --quiet --all --message "Release $TAG" || die "the release commit failed"
git tag --annotate "$TAG" --message "relay-cli $TAG" || die "tagging failed"
note "$TAG on $(git rev-parse --short HEAD)"

IFS=. read -r major minor patch <<<"$VERSION"
NEXT="$major.$minor.$((patch + 1))-SNAPSHOT"
set_version "$NEXT"
git commit --quiet --all --message "Start $NEXT" || die "the next-snapshot commit failed"
note "master now on $NEXT"

step "pushing"
if ! git push --atomic origin "$BRANCH" "$TAG"; then
	die "the push was rejected and NOTHING was published — the tag is local only.
       Usually this means origin/$BRANCH moved while the checks ran. Undo the two
       commits and the tag, pull, and run this again:

       git tag -d $TAG && git reset --hard origin/$BRANCH"
fi

step "published"
note "$TAG is pushed. The release workflow builds and publishes it."
if command -v gh >/dev/null; then
	gh run watch --exit-status 2>/dev/null || true
else
	note "watch it with: gh run watch"
fi
