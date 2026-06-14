//go:build e2e

// Package e2e holds the Docker-backed end-to-end test harness for the twclient
// (SPEC T132 / V114). The Go test (e2e_test.go) drives the high-level net6/net7
// Session against the two bot-populated game servers defined in
// docker-compose.yml. Everything is behind the `e2e` build tag and gated at
// runtime by TW_E2E=1 so it never runs in the normal test suite. See README.md.
package e2e
