// Copyright (c) 2015 Noel Cower. All rights reserved.
// Use of this source code is governed by a simplified
// BSD license that can be found in the LICENSE file.

// Package huprt embodies simple process-restarting logic via a SIGHUP signal. This is similar to
// other Go packages, but only intended to cover the handshake in restarting a process. It does not
// manage HTTP[S] server lifecycles, requests, or anything else.
//
// BUG(ncower): Due to the dependency on Unix signals and the sys/unix package, the huprt package
// is not expected to work on Windows or non-Unix systems. Future work-arounds for this may reduce
// the dependence on signals but require other IPC methods. For now, not supporting Windows is
// acceptable.
package huprt // import "github.com/nilium/huprt"

import (
	"os"
	"os/exec"
	"os/signal"
	"time"

	"golang.org/x/sys/unix"
)

// Process defines an interface for any process that can be killed so that it may be restarted.
// Only one Process is intended to exist per-program.
//
// The BeginRestart method is called first to signal that any resources held by the process should
// be released. This method must block until all critical resources that a new process would
// consume are released (e.g., files, sockets, locks, and others). Non-critical resources can be
// released asynchronously.
//
// Any resources, such as file descriptors, can be passed to the new process by configuring the Cmd
// passed to BeginRestart.
//
// Once BeginRestart has completed, and provided that the Cmd has not been configured incorrectly,
// a new process is started using that Cmd. Once successfully started, the new process will notify
// the old one via SIGTERM that it should exit. At that point, the Kill method is called and the
// program must exit.
//
// If at any point during this process an error occurs, such as if BeginRestart returns an error or
// the new process cannot be started, the Hupd will return an error and allow the program to decide
// how to proceed. The Kill method is never called if an error is returned.
//
// It is particularly important, durring BeginRestart, to stop handling SIGTERM, as Hupd uses this
// to know when to invoke its Kill method.
//
// Essentially, the flow from Hupd.Restart to BeginRestart to Kill behaves roughly like the
// following diagram:
//
//             ┌─In Old Process ───────────────────────────────────────┐
//             │                                                       │
//     SIGHUP  │┌──────────────┐ Prepare  ┌───────────────────┐ Spawn  │    ┌──────────────┐
//     ────────▶│ Old Process  │─────────▶│ BeginRestart(cmd) │─ ─ ─ ─ ┼ ─ ▶│ New Process  │
//             │└──────────────┘          └───────────────────┘        │    └──────────────┘
//             │        ▲  ┌───────────────────┐                       │        │
//             │        └──│      Kill()       │◀─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┼ ─ ─ ─ ─
//             │           └───────────────────┘    Recv SIGTERM       │     Send
//             │                                                       │    SIGTERM
//             └───────────────────────────────────────────────────────┘
//
type Process interface {
	BeginRestart(*exec.Cmd) error
	Kill()
}

// Hupd is responsible for restarting the host process and killing its parent process (if in the
// new process).
type Hupd struct {
	Process

	RestartArg string
	Timeout    time.Duration
}

// Start tells Hupd that the program is starting and whether it's starting up from a process that
// is restarting. If fromRestart is true, the parent process is sent a SIGTERM to tell it to exit.
//
// If an error occurs when sending the SIGTERM, that error is returned.
func (h *Hupd) Start(fromRestart bool) error {
	if !fromRestart {
		return nil
	}

	ppid := os.Getppid()
	if err := unix.Kill(ppid, unix.SIGTERM); err != nil {
		return &Error{ErrKillProcess, err}
	}
	return nil
}

// restartCmd creates and returns an execCmd based on the initial program startup options
// (i.e., cmd.Path is the first CLI argument and all others are passed through as its arguments).
//
// Only the first argument is checked for the restart argument, hupArg. If it isn't already the
// first argument, it is prepended to the argument list. As a result, the arguments for a
// restarting process should always be predictable both for the new process and the Hupd process's
// BeginRestart method.
func restartCmd(hupArg string) exec.Cmd {
	var cmd exec.Cmd
	var binpath = os.Args[0]
	var args []string

	if len(os.Args) > 1 {
		args = make([]string, len(os.Args)+1)
		copy(args[2:], os.Args[1:])
		if args[1] == hupArg {
			args = args[1:]
		} else {
			args[1] = hupArg
		}
		args[0] = binpath
	} else {
		args = []string{binpath, hupArg}
	}

	cmd.Path = binpath
	cmd.Args = args
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}

// NotifyRestart waits for a SIGHUP and, once-received, attempts to restart the process. Returns
// any error that occurs. This function is intended to be run in a separate goroutine, as it will
// block until a SIGHUP is received.
//
// It is effectively a convenience function for calling signal.Notify, waiting for a signal, and
// calling the Hupd Restart method.
func (h *Hupd) NotifyRestart() error {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, unix.SIGHUP)
	defer signal.Stop(hup)

	<-hup
	return h.Restart()
}

// Restart tells Hupd to restart this process. If the Hupd's RestartArg field is empty, the restart
// argument passed to the new process defaults to "-restart". It is assumed to always be the first
// argument. As such, only the first argument is checked for it. If it's not the first argument, it
// is prepended to the argument list passed to the new process.
func (h *Hupd) Restart() error {
	if h.Process == nil {
		return &Error{ErrNoProcess, nil}
	}

	arg := h.RestartArg
	if len(arg) == 0 {
		arg = "-restart"
	}

	cmd := restartCmd(arg)

	if err := h.Process.BeginRestart(&cmd); err != nil {
		return &Error{ErrRestart, err}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, unix.SIGTERM)
	defer signal.Stop(sig)

	if err := cmd.Start(); err != nil {
		return &Error{ErrNewProcess, err}
	}

	// Default to nil so it blocks forever on receive, unless there's a defined timeout.
	var timeout <-chan time.Time
	if h.Timeout > 0 {
		timeout = time.After(h.Timeout)
	}

	select {
	case <-sig:
		h.Process.Kill()
	case <-timeout:
		return &Error{ErrTimeout, nil}
	}

	return nil
}
