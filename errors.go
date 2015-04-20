// Copyright (c) 2015 Noel Cower. All rights reserved.
// Use of this source code is governed by a simplified
// BSD license that can be found in the LICENSE file.

package huprt

// Error represents a huprt error. All errors returned by huprt all contain an
// error code identifying where the error originated from as well as an
// additional inner error that triggered this error.
//
// There are no un-wrapped errors returned by huprt.
type Error struct {
	Code  int
	Inner error
}

const (
	ErrTimeout     int = iota // huprt: process restart timed out
	ErrNewProcess             // huprt: error starting new process
	ErrKillProcess            // huprt: error killing parent process
	ErrRestart                // huprt: restart error
	ErrNoProcess              // huprt: Hupd.Process is nil
)

var errMessages = map[int]string{
	ErrTimeout:     "huprt: process restart timed out",
	ErrNewProcess:  "huprt: error starting new process",
	ErrKillProcess: "huprt: error killing parent process",
	ErrRestart:     "huprt: restart error",
	ErrNoProcess:   "huprt: Hupd.Process is nil",
}

func (e *Error) Error() string {
	if e == nil {
		return "huprt: no error"
	}

	msg, ok := errMessages[e.Code]
	if !ok {
		msg = "huprt: unknown error"
	}

	if e.Inner != nil {
		msg += ": " + e.Inner.Error()
	}

	return msg
}
