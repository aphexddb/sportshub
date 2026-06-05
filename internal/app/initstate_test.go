package app

import "testing"

func TestBootState_Sequence(t *testing.T) {
	b := newBootState("ports", "media")

	// Initial: all pending, not done.
	s := b.snapshot()
	if s.Done {
		t.Fatal("should not be done initially")
	}
	if len(s.Checks) != 2 || s.Checks[0].Name != "ports" || s.Checks[0].Status != "pending" {
		t.Fatalf("unexpected initial checks: %+v", s.Checks)
	}

	b.start("ports", "Freeing ports…")
	if s = b.snapshot(); s.Phase != "Freeing ports…" || s.Checks[0].Status != "running" {
		t.Fatalf("ports should be running with phase set: %+v", s)
	}
	b.ok("ports")
	if s = b.snapshot(); s.Checks[0].Status != "ok" {
		t.Fatalf("ports should be ok: %+v", s.Checks[0])
	}

	b.start("media", "Starting media server…")
	b.fail("media", "boom")
	if s = b.snapshot(); s.Checks[1].Status != "error" || s.Checks[1].Detail != "boom" || s.Error != "boom" {
		t.Fatalf("media failure not recorded: %+v err=%q", s.Checks[1], s.Error)
	}

	// A failed step means finish() must NOT mark Done (overlay stays up on error).
	b.finish()
	if s = b.snapshot(); s.Done || s.Phase != "Startup failed" {
		t.Fatalf("a failed boot must not be Done: %+v", s)
	}
	// Check ordering is preserved.
	if s.Checks[0].Name != "ports" || s.Checks[1].Name != "media" {
		t.Fatalf("check order not preserved: %+v", s.Checks)
	}
}

func TestBootState_SuccessIsDone(t *testing.T) {
	b := newBootState("ports", "media")
	b.start("ports", "Freeing ports…")
	b.ok("ports")
	b.start("media", "Starting media server…")
	b.ok("media")
	b.finish()
	if s := b.snapshot(); !s.Done || s.Phase != "Ready" || s.Error != "" {
		t.Fatalf("a clean boot must be Done/Ready with no error: %+v", s)
	}
}
