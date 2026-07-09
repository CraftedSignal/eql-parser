module github.com/craftedsignal/eql-parser/cmd/generated-corpus

go 1.25.0

require (
	github.com/craftedsignal/eql-parser v0.0.0
	github.com/craftedsignal/sigma-parser v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/craftedsignal/eql-parser => ../..

replace github.com/craftedsignal/sigma-parser => ../../../sigma-parser
