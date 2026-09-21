//go:build wireinject

package app

import "github.com/google/wire"

// Initialize builds the whole object graph. This file is only read by the wire
// tool; the generated implementation lives in wire_gen.go.
func Initialize(cfg Config) (*App, func(), error) {
	wire.Build(providers)
	return nil, nil, nil
}
