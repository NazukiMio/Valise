#!/usr/bin/env bash
# Runs the self-check list from AGENTS.md section 6. Until client/ui/go.mod
# has mygo, `go tool mygo vet` is skipped.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

step() { printf '\n== %s\n' "$*"; }
gocheck() {
	go test ./...
	go vet ./...
	local unformatted
	unformatted="$(gofmt -l .)"
	if [ -n "$unformatted" ]; then
		printf 'gofmt needed:\n%s\n' "$unformatted"
		return 1
	fi
}

step "1. core"
cd "$root/core"
gocheck
# Tests that read kit/ or profiles/ (outside the module) bypass the test
# cache: it does not notice those files changing.
go test -count=1 ./kittools/ ./profile/
go run ./cmd/profilelint ../profiles/*.yaml

step "2. porter + cross builds"
cd "$root/porter"
gocheck
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null .
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null .

step "3. client"
cd "$root/client"
gocheck
go test -count=1 ./profiles/

step "4. testbed"
cd "$root/testbed"
gocheck

step "5. client/ui"
cd "$root/client/ui"
gocheck
if grep -q 'github.com/egoist/mygo' go.mod; then
	go tool mygo vet
else
	echo "(尚未引入 mygo：跳过 mygo vet)"
fi

go test -count=1 -run '^TestFetchPack$' ./service/

printf '\nSELFCHECK_PASS\n'
