package handlers

import (
	"fmt"
	"runtime/debug"
)

// safeGo runs fn in a new goroutine with a panic backstop. The daemon processes
// messages from untrusted peers, and a panic in a per-message handler goroutine
// (a malformed field slipping past validation, a nil deref, etc.) would otherwise
// crash the whole process. Recovering here contains the blast radius to the one
// bad message. label identifies the handler in the recovery log.
func safeGo(label string, fn func()) {
	go func() {
		defer recoverPanic(label)
		fn()
	}()
}

// recoverPanic recovers a panic and logs it with a stack trace. Use as
// `defer recoverPanic("label")` inside a goroutine or a synchronously-invoked
// callback that handles untrusted input.
func recoverPanic(label string) {
	if r := recover(); r != nil {
		fmt.Printf("[Recover] %s panicked: %v\n%s\n", label, r, debug.Stack())
	}
}
