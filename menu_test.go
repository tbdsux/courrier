package main

import (
	"runtime"
	"testing"
)

func TestAccel(t *testing.T) {
	want := "Ctrl+N"
	if runtime.GOOS == "darwin" {
		want = "Super+N"
	}
	if got := accel("⌘N"); got != want {
		t.Errorf("accel(⌘N) = %q, want %q", got, want)
	}
	if got := accel("J"); got != "" {
		t.Errorf("a key without modifier got %q", got)
	}
}
