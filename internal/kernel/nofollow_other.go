//go:build windows

package kernel

// noFollowFlag has no equivalent here, so it contributes nothing to the open
// flags -- and NoFollowSupported says so, which is what callers check.
const noFollowFlag = 0

// NoFollowSupported is false: `repair` refuses to run rather than performing
// descriptor-confined writes it cannot actually confine. The Python kernel
// makes the same refusal for the same reason.
const NoFollowSupported = false
