.PHONY: all test lint fmt clean benchmark coverage fuzz corpus sigma-corpus

all: test

test:
	go test -v -race ./...

lint:
	golangci-lint run --config=.github/golangci.yml

fmt:
	gofmt -s -w .
	goimports -w -local github.com/craftedsignal/eql-parser .

clean:
	go clean -testcache

benchmark:
	go test -bench=. -benchmem ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run each fuzz target for a short bounded time.
fuzz:
	go test -fuzz=FuzzExtractConditions -fuzztime=30s .
	go test -fuzz=FuzzParse -fuzztime=30s .

# Regenerate the differential + large corpora. The generator is a nested
# module (keeps the parser itself dependency-free), so it runs from its own
# directory. REAL_CORPUS points at scraped real EQL used as generation seeds.
corpus:
	cd cmd/generated-corpus && go run . \
		-count $(if $(COUNT),$(COUNT),500) \
		-large $(if $(LARGE),$(LARGE),500000) \
		-real ../../testdata/real_eql_corpus.jsonl

# Regenerate the Sigma<->EQL round-trip corpus from the raw Sigma rule corpus
# in the sibling sigma-parser module. Requires ../sigma-parser to be present.
sigma-corpus:
	cd cmd/generated-corpus && go run . -sigma-roundtrip
