#!/bin/sh
# Usage: scripts/backdate.sh runs/<run>/repo [START_DATE]
# Rewrites every commit in a run's repo (all branches) to fall in a 5-day span,
# in order, between 09:00 and 17:00 each day. START_DATE is YYYY-MM-DD and
# defaults to 6 months ago (UTC). Needs GNU date (Linux). Rewrites history.
set -eu
repo=${1:?usage: backdate.sh runs/<run>/repo [YYYY-MM-DD]}
start=${2:-$(date -u -d '6 months ago' +%F)}
map=$(mktemp)
trap 'rm -f "$map"' EXIT
base=$(date -u -d "$start 09:00" +%s)
cd "$repo"
git rev-list --reverse --topo-order --all | awk -v base="$base" '
  { sha[NR] = $1 }
  END {
    for (i = 1; i <= NR; i++) {
      f = (NR > 1) ? (i - 1) / (NR - 1) : 0
      day = int(f * 4.999)
      within = f * 5 - day; if (within > 1) within = 1
      print sha[i], base + day * 86400 + int(within * 8 * 3600)
    }
  }' > "$map"
FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f --env-filter '
  t=$(awk -v c="$GIT_COMMIT" "\$1 == c { print \$2 }" "'"$map"'")
  GIT_AUTHOR_DATE="$t +0000"; GIT_COMMITTER_DATE="$t +0000"
  export GIT_AUTHOR_DATE GIT_COMMITTER_DATE
' -- --all
git for-each-ref --format='%(refname)' refs/original | xargs -r -n1 git update-ref -d
git log --all --format='%ad  %s' --date=iso | sort | sed -n '1p;$p'
