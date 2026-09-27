package local

import (
	"errors"
	"syscall"
	"time"
)

type SideStatus struct {
	ClaudeWaiting   bool
	ClaudeWaitSince time.Time
	ClaudeSessionID string
	ClaudeCursor    int
	CodexAttached   bool
	CodexThreadName string
	CodexAttachedAt time.Time
}

var PIDAlive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func ReadSideStatus(mailDir string, pidAlive func(int) bool) SideStatus {
	var s SideStatus
	if m, ok := ReadWaitMarker(mailDir, "claude"); ok && pidAlive(m.PID) {
		s.ClaudeWaiting, s.ClaudeWaitSince, s.ClaudeSessionID = true, m.Since, m.SessionID
	}
	s.ClaudeCursor = ReadCursor(mailDir, "claude").Seq
	if a, ok := ReadAttach(mailDir); ok {
		s.CodexAttached, s.CodexThreadName, s.CodexAttachedAt = true, a.Name, a.At
	}
	return s
}
