#!/bin/sh
# Downloads the pinned skill sources into standards/vendor/ and writes
# standards/vendor/lock.json. Change a commit here, run this script, then run
# the eval set before you accept the change.
set -eu
cd "$(dirname "$0")"

PONYTAIL=96608e078b2d5ea607c5c3b8b5a54d1c1fba6846
GO_MODERN=155dc7ca10da5e1f6c841503086957b1b37f5815
ORWELL=1be9984bdafbb68eaff400bdd82773a1d41ee7ec

rm -rf vendor
lock="["
fetch() { # name repo commit path
	mkdir -p "vendor/$1/$(dirname "$4")"
	curl -fsSL "https://raw.githubusercontent.com/$2/$3/$4" -o "vendor/$1/$4"
	sum=$(sha256sum "vendor/$1/$4" | cut -d' ' -f1)
	lock="$lock
  {\"name\": \"$1\", \"repo\": \"$2\", \"commit\": \"$3\", \"path\": \"$4\", \"sha256\": \"$sum\"},"
}

fetch ponytail DietrichGebert/ponytail "$PONYTAIL" LICENSE
fetch ponytail DietrichGebert/ponytail "$PONYTAIL" .agents/rules/ponytail.md
fetch ponytail DietrichGebert/ponytail "$PONYTAIL" skills/ponytail-review/SKILL.md
fetch go-modern JetBrains/go-modern-guidelines "$GO_MODERN" LICENSE
fetch go-modern JetBrains/go-modern-guidelines "$GO_MODERN" internal/guidelines/guidelines.json
fetch orwell tamdogood/builder-essential-skills "$ORWELL" LICENSE
fetch orwell tamdogood/builder-essential-skills "$ORWELL" skills/orwell-writing/SKILL.md

printf '%s\n]\n' "${lock%,}" > vendor/lock.json
echo "vendored $(grep -c sha256 vendor/lock.json) files"
