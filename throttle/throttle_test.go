// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: throttle/throttle_test.go
// About: Tests for package throttle.
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

package throttle

import (
	"math"
	"testing"
	"time"
)

// fakeClock stands still until a test steps it.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time       { return c.now }
func (c *fakeClock) step(d time.Duration) { c.now = c.now.Add(d) }
func newClock() *fakeClock                { return &fakeClock{now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)} }

func newThrottle(t *testing.T, cfg Config) (*Throttle, *fakeClock) {
	t.Helper()
	c := newClock()
	th, err := New(cfg, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	return th, c
}

// recordN records outcome o n times against key.
func recordN(th *Throttle, key string, o Outcome, n int) {
	for i := 0; i < n; i++ {
		th.Record(key, o)
	}
}

func TestUntouchedKeyReadsLow(t *testing.T) {
	th, _ := newThrottle(t, DefaultConfig())
	if r := th.Read("u1"); r != (Reading{Score: 0, Level: 0, Band: BandLow}) {
		t.Fatalf("got %+v", r)
	}
	if th.Len() != 0 {
		t.Fatal("reading a key must not create it")
	}
}

// With a floor of 0, a touched key whose score is back at 0 must still be a
// valid, initialised state (the excerpt treated 0 as "never touched").
func TestFloorZeroScoreIsInitialised(t *testing.T) {
	th, c := newThrottle(t, DefaultConfig())
	th.Record("u1", Acked) // -2, clamped to the floor
	if th.Len() != 1 {
		t.Fatal("record must create the key")
	}
	th.Record("u1", Sent)
	c.step(time.Second)
	if got := th.Read("u1").Score; got != 3 {
		t.Fatalf("score %d, want 3", got)
	}
}

func TestCostsAndClamps(t *testing.T) {
	th, _ := newThrottle(t, DefaultConfig())
	th.Record("u1", Sent)       // 3
	th.Record("u1", Refused)    // 9
	th.Record("u1", MinorFault) // 17
	th.Record("u1", MajorFault) // 32
	th.Record("u1", Acked)      // 30
	if got := th.Read("u1").Score; got != 30 {
		t.Fatalf("score %d, want 30", got)
	}
	recordN(th, "u1", MajorFault, 2) // 60, capped at 50
	if got := th.Read("u1").Score; got != 50 {
		t.Fatalf("score %d, want cap 50", got)
	}
	if got := th.Overflow(); got != 10 {
		t.Fatalf("overflow %d, want 10", got)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	th, _ := newThrottle(t, DefaultConfig())
	recordN(th, "busy", MajorFault, 3)
	if got := th.Read("busy").Band; got != BandCritical {
		t.Fatalf("busy band %s", got)
	}
	if got := th.Read("quiet").Band; got != BandLow {
		t.Fatalf("quiet band %s", got)
	}
}

func TestDecayHalvesExcess(t *testing.T) {
	th, c := newThrottle(t, DefaultConfig())
	recordN(th, "u1", Sent, 16) // 48
	c.step(20 * time.Minute)
	if got := th.Read("u1").Score; got != 24 {
		t.Fatalf("after one half-life %d, want 24", got)
	}
	c.step(40 * time.Minute)
	if got := th.Read("u1").Score; got != 6 {
		t.Fatalf("after three half-lives %d, want 6", got)
	}
}

// Decay is lazy, so reading often must give the same score as reading once.
func TestDecayDoesNotDependOnReadCount(t *testing.T) {
	a, ca := newThrottle(t, DefaultConfig())
	b, cb := newThrottle(t, DefaultConfig())
	recordN(a, "u", MajorFault, 3)
	recordN(b, "u", MajorFault, 3)
	for i := 0; i < 600; i++ {
		ca.step(3 * time.Second)
		a.Read("u")
	}
	cb.step(30 * time.Minute)
	if x, y := a.Read("u").Score, b.Read("u").Score; x != y {
		t.Fatalf("stepped %d, once %d", x, y)
	}
}

func TestBackwardsClockAppliesNothing(t *testing.T) {
	th, c := newThrottle(t, DefaultConfig())
	recordN(th, "u1", MajorFault, 2) // 30
	c.step(-time.Hour)
	if got := th.Read("u1").Score; got != 30 {
		t.Fatalf("score %d, want 30", got)
	}
}

func TestBands(t *testing.T) {
	th, _ := newThrottle(t, DefaultConfig())
	for _, tc := range []struct {
		score int
		band  string
	}{
		{0, BandLow}, {13, BandLow}, {14, BandMedium}, {27, BandMedium},
		{28, BandHigh}, {41, BandHigh}, {42, BandCritical}, {50, BandCritical},
	} {
		if got := bands[th.levelOf(tc.score)]; got != tc.band {
			t.Errorf("score %d: %s, want %s", tc.score, got, tc.band)
		}
	}
}

// The README's table: which priorities each band sends, and what happens to
// the rest.
func TestAllowByBand(t *testing.T) {
	type row struct{ marketing, transactional, high, critical Action }
	for _, tc := range []struct {
		score int
		want  row
	}{
		{0, row{Send, Send, Send, Send}},
		{15, row{Batch, Send, Send, Send}},
		{30, row{Digest, Digest, Send, Send}},
		{45, row{Defer, Defer, Defer, Send}},
	} {
		th, _ := newThrottle(t, DefaultConfig())
		if tc.score > 0 {
			th.keys["u"] = &keyState{score: float64(tc.score), settled: th.clock(), init: true}
		}
		got := row{
			th.Allow("u", Marketing).Action,
			th.Allow("u", Transactional).Action,
			th.Allow("u", High).Action,
			th.Allow("u", Critical).Action,
		}
		if got != tc.want {
			t.Errorf("score %d: %+v, want %+v", tc.score, got, tc.want)
		}
	}
}

// RetryAfter must be the idle time after which the same message is sent.
func TestRetryAfterIsWhenTheMessageWouldSend(t *testing.T) {
	for _, p := range []Priority{Marketing, Transactional, High} {
		th, c := newThrottle(t, DefaultConfig())
		recordN(th, "u", MajorFault, 4) // capped at 50
		d := th.Allow("u", p)
		if d.Action == Send || d.RetryAfter <= 0 {
			t.Fatalf("priority %d: %+v", p, d)
		}
		c.step(d.RetryAfter - time.Second)
		if th.Allow("u", p).Action == Send {
			t.Fatalf("priority %d sent a second early", p)
		}
		c.step(time.Second)
		if got := th.Allow("u", p).Action; got != Send {
			t.Fatalf("priority %d: %v at RetryAfter, want Send", p, got)
		}
	}
}

func TestForecast(t *testing.T) {
	th, c := newThrottle(t, DefaultConfig())
	if _, _, ok := th.Forecast("u"); ok {
		t.Fatal("LOW has no forecast")
	}
	recordN(th, "u", Sent, 10) // 30, HIGH
	band, after, ok := th.Forecast("u")
	if !ok || band != BandMedium {
		t.Fatalf("got %s %v %v", band, after, ok)
	}
	c.step(after)
	if got := th.Read("u").Band; got != BandMedium {
		t.Fatalf("after forecast, band %s", got)
	}

	cfg := DefaultConfig()
	cfg.Horizon = time.Minute
	short, _ := newThrottle(t, cfg)
	recordN(short, "u", Sent, 10)
	if _, _, ok := short.Forecast("u"); ok {
		t.Fatal("a drop past the horizon must report no forecast")
	}
}

func TestSweepDropsCooledKeys(t *testing.T) {
	th, c := newThrottle(t, DefaultConfig())
	th.Record("cool", Sent)
	recordN(th, "hot", MajorFault, 3)
	c.step(2 * time.Hour)
	if n := th.Sweep(); n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	if th.Len() != 1 || th.Read("hot").Score == 0 {
		t.Fatal("the hot key must survive")
	}
}

func TestConfigValidation(t *testing.T) {
	bad := DefaultConfig()
	bad.Thresholds = [3]int{28, 14, 42}
	if _, err := New(bad, nil); err == nil {
		t.Fatal("descending thresholds accepted")
	}
	bad = DefaultConfig()
	bad.HalfLife = 0
	if _, err := New(bad, nil); err == nil {
		t.Fatal("zero half-life accepted")
	}
}

// loopSecondsBelow is the excerpt's search: step one second at a time until
// the score reads below threshold.
func loopSecondsBelow(cfg Config, score float64, threshold, limit int) int {
	e := score - float64(cfg.Floor)
	for s := 1; s <= limit; s++ {
		t := float64(cfg.Floor) + e*math.Exp2(-float64(s)/cfg.HalfLife.Seconds())
		if int(math.Round(t)) < threshold {
			return s
		}
	}
	return -1
}

// closedForm is the README's formula with no correction step.
func closedForm(cfg Config, score float64, threshold int) int {
	e := score - float64(cfg.Floor)
	d := float64(threshold) - 0.5 - float64(cfg.Floor)
	return int(math.Floor(cfg.HalfLife.Seconds()*math.Log2(e/d))) + 1
}

// The closed form must agree with the excerpt's loop, both raw (as the README
// claims) and as the throttle uses it. Checked for the excerpt's own model
// and for the default one.
func TestClosedFormMatchesLoop(t *testing.T) {
	excerpt := Config{
		Floor: 54, Cap: 104, Thresholds: [3]int{68, 82, 96},
		HalfLife: 40 * time.Second, Horizon: 600 * time.Second,
	}
	short := DefaultConfig()
	short.HalfLife = 40 * time.Second // keeps the loop short
	for _, cfg := range []Config{excerpt, short} {
		th, _ := newThrottle(t, cfg)
		limit := 40 * int(cfg.HalfLife.Seconds())
		samples, raw := 0, 0
		for score := float64(cfg.Thresholds[0]) - 0.5; score <= float64(cfg.Cap); score += 0.01 {
			st := &keyState{score: score, init: true}
			level := th.levelOf(th.whole(st))
			if level == 0 {
				continue
			}
			threshold := cfg.Thresholds[level-1]
			want := loopSecondsBelow(cfg, score, threshold, limit)
			if got := th.secondsBelowLocked(st, threshold); got != want {
				t.Fatalf("floor %d score %.2f: throttle %d, loop %d", cfg.Floor, score, got, want)
			}
			if closedForm(cfg, score, threshold) != want {
				raw++
			}
			samples++
		}
		if raw != 0 {
			t.Errorf("floor %d: raw closed form disagreed on %d of %d samples", cfg.Floor, raw, samples)
		}
	}
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=9a337fc46d2df7bd23f56cca896620220861163a45c3e8366749bbb2bf83cac5 ====
