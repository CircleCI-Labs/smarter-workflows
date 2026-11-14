// Package monorepo holds an example continuation config for a small monorepo,
// the impact map of its last traced full run, and scenario tests: for each
// kind of change, the plan and the emitted config are checked against golden
// files in scenarios/. Refresh them with
//
//	go test ./examples/monorepo -run TestScenarios -update
package monorepo
