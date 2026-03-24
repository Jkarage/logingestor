run:
	go run api/services/INGESTOR/main.go | go run api/tooling/logfmt/main.go

# Vendor 
tidy:
	go mod tidy
	go mod vendor

pgcli:
	pgcli postgres://postgres:postgres@12.13.14.15:5432


# ==============================================================================
# Database Tests

# ==============================================================================
# Class Stuff

run-auth:
	go run api/services/auth/main.go | go run api/tooling/logfmt/main.go

run:
	go run api/services/sales/main.go | go run api/tooling/logfmt/main.go

run-help:
	go run api/services/sales/main.go --help | go run api/tooling/logfmt/main.go

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