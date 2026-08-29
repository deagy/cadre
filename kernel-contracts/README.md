# Vendored kernel contracts

**Not authoritative.** These files are copies. `lifecycle-gates.json` and
`mutation-gates.json` are owned by the lifecycle kernel, which lives in its own
repository and ships them with its releases.

They are here because several tests read the contracts *as data* — one of
exactly two couplings the kernel boundary permits, the other being shelling out
to the kernel binary. A test that shelled out would need a kernel installed to
run, which would make the suite non-hermetic; a test that fetched them would
make it non-offline. So they are vendored, and a drift check holds them to the
source.

`TestVendoredKernelContractsMatchTheKernel` compares this directory against the
kernel's own copy. It skips when no kernel source is reachable and fails under
CI, where it must not be skipped — the same shape as
`internal/orchestration/schema_release_drift_test.go`, and for the same reason:
a guard that silently checks nothing is worse than no guard.

To change a contract, change it in the kernel and re-vendor here. Editing these
files directly makes the drift check fail, which is the point.
