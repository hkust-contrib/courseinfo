.PHONY: setup build test test-native test-worker test-smoke check check-infra dev plan

# Prerequisites: Node 24, Bun 1.4.3, rustup, Terraform 1.9.8; on Linux,
# worker-build installation also requires pkg-config and OpenSSL development headers.
setup:
	rustup show
	cargo install worker-build --version 0.8.7 --locked
	bun install --frozen-lockfile

build:
	bun run build

test-native:
	bun run test:native

# Runtime tests consume the production files; Wrangler must not rebuild them.
test-worker: build
	bun run test:worker

test-smoke:
	bun run test:smoke

test: test-native test-worker test-smoke

check-infra:
	terraform -chdir=deployment fmt -check
	terraform -chdir=deployment init -backend=false -lockfile=readonly -input=false
	terraform -chdir=deployment validate -no-color

check: test check-infra
	cargo fmt --all -- --check
	cargo clippy --workspace --target wasm32-unknown-unknown --locked -- -D warnings

dev: build
	bun run dev

# Requires explicit real TF_VAR_cloudflare_account_id and dedicated TF_HTTP_*
# configuration. Does not apply or activate resources.
plan: build
	terraform -chdir=deployment init -lockfile=readonly -input=false
	terraform -chdir=deployment plan -input=false -lock-timeout=5m
