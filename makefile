# Vendor 
tidy:
	go mod tidy
	go mod vendor

pgcli:
	pgcli postgres://postgres:Hu88e1na%408@46.62.196.184:5432


# ==============================================================================
# Database Tests
#
# Unit tests are hermetic. Integration tests (business/sdk/dbtest) create a
# throwaway database per test on whatever server TEST_DB_HOST names, migrate it,
# and drop it afterwards; without TEST_DB_HOST they skip. Never point these at
# production: the tests only ever create and drop their own databases, but the
# server still carries their load.

TEST_DB_PORT ?= 55432
TEST_DB_HOST ?= 127.0.0.1:$(TEST_DB_PORT)
TEST_DB_USER ?= postgres
TEST_DB_PASSWORD ?= postgres
TEST_DB_DIR ?= /tmp/logingestor-pgtest

TEST_DB_ENV = TEST_DB_HOST=$(TEST_DB_HOST) TEST_DB_USER=$(TEST_DB_USER) \
	TEST_DB_PASSWORD=$(TEST_DB_PASSWORD) TEST_DB_DISABLE_TLS=true

test:
	go test ./... -count=1

test-integration:
	$(TEST_DB_ENV) go test ./... -count=1 -run Integration -v

test-all: test-integration
	$(TEST_DB_ENV) go test ./... -count=1

# A throwaway local cluster, so integration tests need no shared server. With
# Docker available this is equivalent:
#   docker run --rm -d -p $(TEST_DB_PORT):5432 -e POSTGRES_PASSWORD=$(TEST_DB_PASSWORD) postgres:16
# The socket lives in /tmp because a long socket directory exceeds the 103 byte
# limit Postgres allows for it.
test-db-up:
	@test -d $(TEST_DB_DIR)/data || initdb -D $(TEST_DB_DIR)/data -U $(TEST_DB_USER) --auth=trust -E UTF8 > /dev/null
	@pg_ctl -D $(TEST_DB_DIR)/data \
		-o "-p $(TEST_DB_PORT) -k /tmp -c listen_addresses=127.0.0.1" \
		-l $(TEST_DB_DIR)/server.log start
	@echo "test postgres listening on $(TEST_DB_HOST)"

test-db-down:
	@pg_ctl -D $(TEST_DB_DIR)/data stop || true

test-db-clean: test-db-down
	rm -rf $(TEST_DB_DIR)

# ==============================================================================
# Class Stuff

run-auth:
	go run api/services/auth/main.go | go run api/tooling/logfmt/main.go

run:
	go run api/services/ingestor/main.go | go run api/tooling/logfmt/main.go

run-help:
	go run api/services/ingestor/main.go --help | go run api/tooling/logfmt/main.go

curl:
	curl -i http://localhost:3000/v1/hack

curl-auth:
	curl -i -H "Authorization: Bearer ${TOKEN}" http://localhost:3000/v1/hackauth

load-hack:
	hey -m GET -c 100 -n 100000 "http://localhost:3000/v1/hack"

admin:
	go run api/tooling/admin/main.go

ready:
	curl -i http://localhost:3000/v1/readiness

live:
	curl -i http://localhost:3000/v1/liveness

curl-create:
	curl -i -X POST \
	-H "Authorization: Bearer ${TOKEN}" \
	-H 'Content-Type: application/json' \
	-d '{"name":"bill","email":"b@gmail.com","roles":["ADMIN"],"department":"ITO","password":"123","passwordConfirm":"123"}' \
	http://localhost:3000/v1/users

source-env:
	@eval $$(sed -e '/^\s*#/d' -e 's/^/export /' .env) && \
    echo "DB USER: $$INGESTOR_DB_USER"