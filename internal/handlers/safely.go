package handlers

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// safely runs f and returns its error, or an error in place of a panic in it.
//
// A read from the gateway that runs on a goroutine of its own goes through
// it. net/http recovers a panic on the request's goroutine, so a bug in
// decoding an answer fails that one page load; on any other goroutine the
// same panic takes the whole server down.
func safely(what string, f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("panic in "+what, "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("%s: panic: %v", what, p)
		}
	}()
	return f()
}
