// Command fab spins up real, seeded, throwaway local resources —
// databases, caches, SSH hosts, observability backends, and stateful
// HTTP-API mocks — and returns connection credentials. Docker-backed
// via testcontainers-go, with an optional Kubernetes target.
//
// fab prints credentials and gets out of the way; what you do with
// them is your business.
package main

import "github.com/dumbmachine/fabricate/cli"

func main() { cli.Execute() }
