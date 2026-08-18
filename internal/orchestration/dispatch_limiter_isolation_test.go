package orchestration

import (
	"testing"
)

// dispatchLimiter is process-wide, and tests share a process.
//
// The limiter caps how many children this process runs at once, across every
// dispatch path. That is right in production: the resource it protects is the
// machine, not one call.
//
// In a test binary it makes every dispatching test depend on how many
// dispatches ran before it. An async dispatch takes a slot and releases it
// when its child finishes, which in a test may be never -- so after three of
// them, every later dispatch in that process is denied:
//
//	async dispatch returned status "denied", want 'dispatched_async'
//	reason: too many concurrent dispatches (limit 3); retry later
//
// TestPollDispatchStatus failed in CI this way. It is not timing: `go test
// -count=30` reproduces it on the fourth iteration, every time. It passes
// alone, which is why it reads as a mystery rather than as ordering.
//
// withFreshDispatchLimiter gives a test its own limiter and restores the
// previous one, so a test that dispatches neither inherits a poisoned pool nor
// poisons the tests after it.
//
// The variable is an atomic pointer for this reason. Swapping a plain package
// var races with the dispatch paths reading it from goroutines -- caught by
// `go test -race`, invisible without it, and merged once already because I ran
// the suite without the flag CI uses.

// withFreshDispatchLimiter swaps in an empty limiter for the duration of t.
//
// Not a Reset() on the shared one: two tests running in parallel would then
// clear each other's slots mid-flight, turning an ordering bug into a race.
// Each test gets its own.
func withFreshDispatchLimiter(t *testing.T) {
	t.Helper()
	previous := dispatchLimiter.Swap(NewConcurrencyLimiter(MaxConcurrentChildren))
	t.Cleanup(func() { dispatchLimiter.Store(previous) })
}

func TestTheLimiterRefusesBeyondItsCapAndRecovers(t *testing.T) {
	// The cap's own behaviour, which nothing asserted -- it was only ever
	// observed by accident, as a neighbouring test failing.
	limiter := NewConcurrencyLimiter(2)

	if !limiter.TryAcquire() {
		t.Fatal("the first slot was refused")
	}
	if !limiter.TryAcquire() {
		t.Fatal("the second slot was refused")
	}
	if limiter.TryAcquire() {
		t.Error("a third slot was granted past a cap of two; the limiter is not " +
			"capping, and every caller past the limit would spawn anyway")
	}

	// Releasing one makes exactly one available again -- not all of them.
	limiter.Release()
	if !limiter.TryAcquire() {
		t.Error("no slot became available after a release")
	}
	if limiter.TryAcquire() {
		t.Error("releasing one slot made more than one available")
	}
}

func TestAFreshLimiterStartsEmpty(t *testing.T) {
	// What withFreshDispatchLimiter buys, checked without touching the shared
	// pool.
	//
	// The first version of this test drained the process-wide limiter to prove
	// a fresh one was unaffected. That is a dangerous way to prove it:
	// Acquire() *blocks* on a full semaphore, so a drained shared pool can
	// hang anything that later blocks on it -- and it did, turning `go test
	// -count=2` into a 600s timeout rather than a failure, which is why the
	// first greps for "--- FAIL" found nothing.
	//
	// The property is about the replacement being empty and the original being
	// restored. Neither needs the shared pool emptied.
	before := dispatchLimiter.Load()

	func() {
		withFreshDispatchLimiter(t)
		fresh := dispatchLimiter.Load()

		if fresh == before {
			t.Fatal("withFreshDispatchLimiter did not replace the limiter")
		}
		for i := 0; i < MaxConcurrentChildren; i++ {
			if !fresh.TryAcquire() {
				t.Fatalf("a fresh limiter refused slot %d of %d; it did not start empty",
					i+1, MaxConcurrentChildren)
			}
		}
		for i := 0; i < MaxConcurrentChildren; i++ {
			fresh.Release()
		}
	}()
}

func TestPollDispatchStatusIsNotOrderDependent(t *testing.T) {
	stubRunner(t)
	// The regression this file is named for. Dispatching more times than the
	// cap inside one test must not deny the later ones, because each gets a
	// fresh pool.
	//
	// Without the helper this fails on the fourth iteration; with it, all of
	// them dispatch.
	for i := 0; i < MaxConcurrentChildren+3; i++ {
		func() {
			withFreshDispatchLimiter(t)
			result := DispatchSecureCloudRole(
				testRoots(t, "code-reviewer"),
				"code-reviewer", "test brief", ModePlanningOnly, "public",
				"", "task123", "session123", "public", DefaultRunner, false,
			)
			status, _ := result["status"].(string)
			if status != "dispatched_async" {
				t.Fatalf("dispatch %d returned %q (reason %v), want dispatched_async",
					i, status, result["reason"])
			}
		}()
	}
}
