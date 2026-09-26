.PHONY: build run test check format

build:
	cd tui && go build -o ../bin/triage-o-mator .

# The TUI runs against an install, never the checkout: make run ROOT=/path/to/repository/triage-o-mator
run:
	cd tui && go run . $(if $(ROOT),--root $(ROOT))

test:
	cd tui && go test ./...
	python3 -c 'import unittest; suite = unittest.defaultTestLoader.discover("tests"); assert suite.countTestCases(), "no Python tests discovered"; result = unittest.TextTestRunner(verbosity=2).run(suite); raise SystemExit(not result.wasSuccessful())'

check: test
	cd tui && go vet ./... && gopls check *.go
	prettier --check README.md AGENTS.md PITCH.md prompts/*.md docs/*.md themes/*.json themes/README.md
	test -z "$$(gofmt -l tui)"

format:
	gofmt -w tui
	prettier --write README.md AGENTS.md PITCH.md prompts/*.md docs/*.md themes/*.json themes/README.md
