// Package monorepo holds an example continuation config for a small monorepo,
// a golden impact map, and scenario tests: for each
// kind of change, the plan and the emitted config are checked against golden
// files in scenarios/. The catalog of the example (catalog.json) is a golden
// file too. Refresh them all with
//
//	go test ./examples/monorepo -update
package monorepo
