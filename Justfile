# run the project in development mode with auto-reload on changes
dev:
	reflex \
		-s \
		-r '(\.go$|\.sql$)' \
		-R '(^|/)\.git/' \
		-R '(^|/)\.direnv/' \
		-R '\.db(-shm|-wal)?$' \
		-R '\.gen\.go$' \
		-- just run-dev

# Generate Go code (sqlc)
generate:
	sqlc generate

# run all offline unit tests
test:
	go test -v ./... -skip=Example

# build hypnoview package with nix
build:
	nix build .#default

# run nix flake check (including NixOS VM test)
check:
	nix flake check -L

# run the server in dev mode (serves templates & static files from disk if --dev is set)
[private]
run-dev:
	go run ./cmd/hypnoview --listen-addr 127.0.0.1:18080 --db hypnoview.db --no-poller --dev

# seed the local database with the most recent 10,000 posts
seed:
	go run ./cmd/hypnoview --seed-only --min-posts 10000 --db hypnoview.db
