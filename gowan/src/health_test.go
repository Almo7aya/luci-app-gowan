// health_test.go
package main

import "testing"

func TestHealthStateStartsUp(t *testing.T) {
	s := new_health_state(3, 2)
	if !s.up {
		t.Fatal("state machine must start UP (optimistic)")
	}
}

func TestFailThresholdFlipsExactlyOnce(t *testing.T) {
	s := new_health_state(3, 2)

	if s.observe(false) {
		t.Fatal("flip after 1 failure, threshold is 3")
	}
	if s.observe(false) {
		t.Fatal("flip after 2 failures, threshold is 3")
	}
	if !s.observe(false) {
		t.Fatal("no flip after 3 failures")
	}
	if s.up {
		t.Fatal("state must be DOWN after crossing fail threshold")
	}
	if s.observe(false) {
		t.Fatal("second flip while already DOWN")
	}
}

func TestRiseThresholdFlipsExactlyOnce(t *testing.T) {
	s := new_health_state(1, 2)
	s.observe(false) // now DOWN

	if s.observe(true) {
		t.Fatal("flip after 1 success, rise threshold is 2")
	}
	if !s.observe(true) {
		t.Fatal("no flip after 2 successes")
	}
	if !s.up {
		t.Fatal("state must be UP after crossing rise threshold")
	}
	if s.observe(true) {
		t.Fatal("second flip while already UP")
	}
}

func TestFlappingResetsCounters(t *testing.T) {
	s := new_health_state(3, 2)

	// fail, fail, ok, fail, fail — never 3 consecutive failures
	s.observe(false)
	s.observe(false)
	s.observe(true)
	s.observe(false)
	if s.observe(false) {
		t.Fatal("flipped although the success reset the failure streak")
	}
	if !s.up {
		t.Fatal("must still be UP")
	}
	if !s.observe(false) {
		t.Fatal("3rd consecutive failure after reset must flip")
	}
}

func TestThresholdsClampToOne(t *testing.T) {
	s := new_health_state(0, -5)
	if !s.observe(false) {
		t.Fatal("fail threshold clamped to 1 must flip on first failure")
	}
	if !s.observe(true) {
		t.Fatal("rise threshold clamped to 1 must flip on first success")
	}
}
