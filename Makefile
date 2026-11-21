.PHONY: build run test lint check format ready

PRETTIER_FILES = README.md AGENTS.md PITCH.md prompts/*.md docs/*.md themes/*.json themes/README.md tests-playbooks/*.md tests-playbooks/seed/*.md .github/workflows/*.yml

build:
	cd tui && go build -o ../bin/triage-o-mator .

# The TUI runs against an install, never the checkout: make run ROOT=/path/to/repository/triage-o-mator
run:
	cd tui && go run . $(if $(ROOT),--root $(ROOT))

test:
	cd tui && go test -list '^Test' | grep -q '^Test'
	cd tui && go test ./...
	python3 -c 'import unittest; suite = unittest.defaultTestLoader.discover("tests"); assert suite.countTestCases(), "no Python tests discovered"; result = unittest.TextTestRunner(verbosity=2).run(suite); raise SystemExit(not result.wasSuccessful())'

# Read-only static checks; they run before the slower tests so formatting and vet failures surface first.
lint:
	cd tui && go vet ./... && gopls check *.go
	prettier --check $(PRETTIER_FILES)
	test -z "$$(gofmt -l tui)"

# The gate, also run by CI; it never rewrites files.
check: lint test

format:
	gofmt -w tui
	prettier --write $(PRETTIER_FILES)

# Local convenience: rewrite formatting, then run the read-only gate. CI runs check, never this.
ready: format check
