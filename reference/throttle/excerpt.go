//go:build ignore

// reference excerpt, not built.
//
// The load-score throttle as first written: one process-wide score that
// rises with each event, decays toward a floor while idle, and is read in
// four bands. Kept as reading material only. README.md describes the per-key
// design the dispatcher should use; PROVENANCE.md (project root) maps each
// section below to its source.
package throttle

import (
	"math"
	"strings"
	"sync"
	"time"
)

// --- source C: band and meter names

const (
	// the four bands, lowest first.
	bandLow      = "LOW"
	bandMedium   = "MEDIUM"
	bandHigh     = "HIGH"
	bandCritical = "CRITICAL"

	pipFull  = "o"
	pipEmpty = "."
)

// --- source B: the score, its costs, and the level thresholds

const (
	scoreFloor     = 54  // resting score, nothing recorded
	scoreCap       = 104 // highest score the throttle reports
	costPerEvent   = 3   // each recorded event
	costRefused    = 6   // a refused event costs more
	costMinorFault = 8   // a minor delivery fault
	costMajorFault = 15  // a major delivery fault
	creditCleanAck = -2  // a clean acknowledgement lowers the score
)

// state is the single global score. score is fractional so continuous decay
// is not lost to rounding; it is read in whole points.
var state struct {
	sync.Mutex
	score    float64   // 0 = never touched; reads as scoreFloor
	settled  time.Time // when idle time was last applied
	overflow int       // score the cap turned away, banked toward a shed
	shedDue  bool      // a shed happened; the next turn records but does not send
}

// current returns the score with idle decay applied.
func current() int {
	state.Lock()
	defer state.Unlock()
	settleLocked()
	return readLocked()
}

func readLocked() int {
	if state.score == 0 {
		return scoreFloor
	}
	return int(math.Round(state.score))
}

// add moves the score by delta and keeps it within [scoreFloor, scoreCap].
// Anything above the cap is banked as overflow, up to overflowCap.
func add(delta int) {
	state.Lock()
	defer state.Unlock()
	settleLocked()
	if state.score == 0 {
		state.score = scoreFloor
	}
	state.score += float64(delta)
	if state.score < scoreFloor {
		state.score = scoreFloor
	}
	if over := state.score - scoreCap; over > 0 {
		state.overflow += int(math.Round(over))
		if state.overflow > overflowCap {
			state.overflow = overflowCap
		}
		state.score = scoreCap
	}
}

// reset returns the throttle to its zero state. Tests call it.
func reset() {
	state.Lock()
	defer state.Unlock()
	state.score = 0
	state.settled = time.Time{}
	state.overflow = 0
	state.shedDue = false
}

// currentLevel buckets the live score into 0..3.
func currentLevel() int { return levelOf(current()) }

// levelOf buckets a score into 0..3. It is the only copy of the thresholds;
// the bands and the forecast both read it.
func levelOf(s int) int {
	switch {
	case s >= 96:
		return 3
	case s >= 82:
		return 2
	case s >= 68:
		return 1
	}
	return 0
}

// --- source A: decay, bands, forecast, and the overflow shed

const (
	halfLife        = 40 * time.Second // the excess over scoreFloor halves in this much idle time
	overflowCap     = 24               // a full bank; the next closeTurn sheds
	shedFloor       = 82               // a shed drops the score to the HIGH threshold
	forecastHorizon = 600              // seconds the forecast looks ahead
	meterWidth      = 6                // overflow meter width, in pips
)

// clock is a var so tests can hold time still or step it forward.
var clock = time.Now

// settleLocked applies the idle time since the score was last read. Callers
// hold state. A clock that runs backwards applies nothing.
func settleLocked() {
	now := clock()
	if state.settled.IsZero() || state.score == 0 {
		state.settled = now
		return
	}
	dt := now.Sub(state.settled)
	if dt <= 0 {
		return
	}
	state.settled = now
	if excess := state.score - scoreFloor; excess > 0 {
		state.score = scoreFloor + excess*math.Exp2(-float64(dt)/float64(halfLife))
	}
}

// bands names the four levels levelOf returns, lowest first.
var bands = [...]string{bandLow, bandMedium, bandHigh, bandCritical}

// bandOf names a level.
func bandOf(level int) string {
	if level < 0 || level >= len(bands) {
		return bandLow
	}
	return bands[level]
}

// reading is the throttle at one moment.
type reading struct {
	score    int    // whole points
	level    int    // 0..3
	band     string // the level's name
	overflow int    // 0..overflowCap
}

// read returns the current reading, idle decay applied.
func read() reading {
	state.Lock()
	defer state.Unlock()
	settleLocked()
	s := readLocked()
	l := levelOf(s)
	return reading{score: s, level: l, band: bandOf(l), overflow: state.overflow}
}

// forecast reports how many seconds of idle time would drop the score one
// band, and which band it would land in. ok is false at LOW (there is
// nothing lower) and past forecastHorizon.
func forecast() (band string, secs int, ok bool) {
	state.Lock()
	defer state.Unlock()
	settleLocked()
	if state.score == 0 {
		return "", 0, false
	}
	now := levelOf(readLocked())
	if now == 0 {
		return "", 0, false
	}
	excess := state.score - scoreFloor
	for s := 1; s <= forecastHorizon; s++ {
		t := scoreFloor + excess*math.Exp2(-float64(s)/halfLife.Seconds())
		if next := levelOf(int(math.Round(t))); next < now {
			return bandOf(next), s, true
		}
	}
	return "", 0, false
}

// closeTurn ends a turn and sheds the score if the overflow bank is full.
func closeTurn() (shed bool) {
	state.Lock()
	defer state.Unlock()
	settleLocked()
	if state.overflow < overflowCap {
		return false
	}
	state.overflow = 0
	if state.score > shedFloor {
		state.score = shedFloor
	}
	state.shedDue = true
	return true
}

// openTurn starts a turn and reports whether it is the one a shed owes:
// recorded, not sent. Asking clears the debt.
func openTurn() (suppressed bool) {
	state.Lock()
	defer state.Unlock()
	suppressed = state.shedDue
	state.shedDue = false
	return suppressed
}

// overflowMeter draws the bank as meterWidth pips, rounding up so that any
// overflow at all lights at least one.
func overflowMeter(overflow int) string {
	lit := (overflow*meterWidth + overflowCap - 1) / overflowCap
	if lit > meterWidth {
		lit = meterWidth
	}
	return strings.Repeat(pipFull, lit) + strings.Repeat(pipEmpty, meterWidth-lit)
}
