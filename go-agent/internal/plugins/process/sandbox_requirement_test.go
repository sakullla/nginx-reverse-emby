//go:build integration

package process

func testSandboxRequirement(budget Budget, privileged, networkBound bool) SandboxRequirement {
	return SandboxRequirement{packageDigest: "test-package", budget: budget, privileged: privileged, networkBound: networkBound}
}
