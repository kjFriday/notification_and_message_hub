// Package throttle is the frequency-capping stage of the dispatcher.
//
// Each recipient key (a user ID, or a user ID plus a channel) has a load
// score. Every dispatch outcome moves the score, idle time lets the excess
// above the floor decay, and the score's band decides which priorities may
// still be sent. Decay is applied lazily on every read or write, so no timers
// run. See reference/throttle/README.md for the design.
package throttle

import (
	"errors"
	"math"
	"sync"
	"time"
)

// Band names, lowest first.
const (
	BandLow      = "LOW"
	BandMedium   = "MEDIUM"
	BandHigh     = "HIGH"
	BandCritical = "CRITICAL"
)

var bands = [...]string{BandLow, BandMedium, BandHigh, BandCritical}

// Priority is how important a message is, lowest first.
type Priority int

const (
	Marketing Priority = iota
	Transactional
	High
	Critical // 2FA, password resets: never throttled
)

// Outcome is what happened to one dispatch attempt or batch.
type Outcome int

const (
	Sent       Outcome = iota // handed to the provider
	Refused                   // the provider refused it
	MinorFault                // a minor delivery fault
	MajorFault                // a major delivery fault
	Acked                     // a clean delivery acknowledgement
)

// Action is what the dispatcher should do with a message.
type Action int

const (
	Send   Action = iota
	Batch         // hold for the next marketing batch
	Digest        // fold into the next digest
	Defer         // hold until the key cools down
)

// Config holds the score model. Scores are in whole points; the thresholds
// are the only copy of the band boundaries.
type Config struct {
	Floor      int    // resting score
	Cap        int    // highest score held
	Thresholds [3]int // lowest score of MEDIUM, HIGH and CRITICAL
	HalfLife   time.Duration
	Horizon    time.Duration // how far Forecast looks ahead
	Costs      [5]int        // indexed by Outcome
}

// DefaultConfig is the excerpt's model moved to a floor of 0, with a
// half-life suited to per-recipient notifications.
func DefaultConfig() Config {
	return Config{
		Floor:      0,
		Cap:        50,
		Thresholds: [3]int{14, 28, 42},
		HalfLife:   20 * time.Minute,
		Horizon:    2 * time.Hour,
		Costs: [5]int{
			Sent:       3,
			Refused:    6,
			MinorFault: 8,
			MajorFault: 15,
			Acked:      -2,
		},
	}
}

func (c Config) validate() error {
	t := c.Thresholds
	switch {
	case c.HalfLife <= 0:
		return errors.New("throttle: half-life must be positive")
	case !(c.Floor < t[0] && t[0] < t[1] && t[1] < t[2] && t[2] <= c.Cap):
		return errors.New("throttle: need floor < thresholds (ascending) <= cap")
	}
	return nil
}

type keyState struct {
	score   float64   // fractional so continuous decay is not lost to rounding
	settled time.Time // when idle time was last applied
	init    bool      // the floor can be 0, so 0 cannot mean "never touched"
}

// Throttle holds one score per key.
type Throttle struct {
	mu       sync.Mutex
	keys     map[string]*keyState
	clock    func() time.Time
	cfg      Config
	overflow int // points the cap turned away, all keys; a metric only
}

// New returns a throttle. clock may be nil, meaning time.Now; tests pass
// their own to hold time still or step it forward.
func New(cfg Config, clock func() time.Time) (*Throttle, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = time.Now
	}
	return &Throttle{keys: make(map[string]*keyState), clock: clock, cfg: cfg}, nil
}

// Reading is one key's throttle at one moment.
type Reading struct {
	Score int    // whole points
	Level int    // 0..3
	Band  string // the level's name
}

// Decision is the answer to Allow.
type Decision struct {
	Action     Action
	Band       string
	RetryAfter time.Duration // for Batch, Digest and Defer: idle time until the priority is sent
}

// Allow reports what to do with a message of priority p for key. It does not
// move the score; call Record with the outcome afterwards.
func (t *Throttle) Allow(key string, p Priority) Decision {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.settleLocked(key)
	level := t.levelOf(t.whole(st))
	d := Decision{Action: Send, Band: bands[level]}
	if p == Critical {
		return d
	}
	top := maxLevel(p)
	if level <= top {
		return d
	}
	d.Action = [...]Action{Send, Batch, Digest, Defer}[level]
	d.RetryAfter = time.Duration(t.secondsBelowLocked(st, t.cfg.Thresholds[top])) * time.Second
	return d
}

// maxLevel is the highest level at which priority p is still sent.
func maxLevel(p Priority) int {
	switch p {
	case Marketing:
		return 0
	case Transactional:
		return 1
	}
	return 2
}

// Record applies the cost of outcome o to key.
func (t *Throttle) Record(key string, o Outcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.settleLocked(key)
	if st == nil {
		st = &keyState{score: float64(t.cfg.Floor), settled: t.clock(), init: true}
		t.keys[key] = st
	}
	st.score += float64(t.cfg.Costs[o])
	if floor := float64(t.cfg.Floor); st.score < floor {
		st.score = floor
	}
	if over := st.score - float64(t.cfg.Cap); over > 0 {
		t.overflow += int(math.Round(over))
		st.score = float64(t.cfg.Cap)
	}
}

// Read returns key's current reading, idle decay applied.
func (t *Throttle) Read(key string) Reading {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.whole(t.settleLocked(key))
	l := t.levelOf(s)
	return Reading{Score: s, Level: l, Band: bands[l]}
}

// Forecast reports how much idle time would drop key one band, and which
// band it would land in. ok is false at LOW, where there is nothing lower,
// and past the horizon.
func (t *Throttle) Forecast(key string) (band string, after time.Duration, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.settleLocked(key)
	level := t.levelOf(t.whole(st))
	if level == 0 {
		return "", 0, false
	}
	secs := t.secondsBelowLocked(st, t.cfg.Thresholds[level-1])
	if after = time.Duration(secs) * time.Second; after > t.cfg.Horizon {
		return "", 0, false
	}
	return bands[level-1], after, true
}

// Overflow is the total score the cap has turned away, across all keys.
func (t *Throttle) Overflow() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overflow
}

// Sweep drops keys that have decayed back to the floor and reports how many
// it dropped. A dropped key reads exactly as it did before.
func (t *Throttle) Sweep() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for key := range t.keys {
		if t.whole(t.settleLocked(key)) == t.cfg.Floor {
			delete(t.keys, key)
			n++
		}
	}
	return n
}

// Len is the number of keys held.
func (t *Throttle) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.keys)
}

// settleLocked applies the idle time since key was last read and returns its
// state, or nil if key has none. A clock that runs backwards applies nothing.
func (t *Throttle) settleLocked(key string) *keyState {
	st := t.keys[key]
	if st == nil || !st.init {
		return nil
	}
	now := t.clock()
	dt := now.Sub(st.settled)
	if dt <= 0 {
		return st
	}
	st.settled = now
	floor := float64(t.cfg.Floor)
	if excess := st.score - floor; excess > 0 {
		st.score = floor + excess*math.Exp2(-float64(dt)/float64(t.cfg.HalfLife))
	}
	return st
}

// whole reads a state in whole points; no state reads as the floor.
func (t *Throttle) whole(st *keyState) int {
	if st == nil {
		return t.cfg.Floor
	}
	return int(math.Round(st.score))
}

// levelOf buckets a score into 0..3.
func (t *Throttle) levelOf(s int) int {
	level := 0
	for _, th := range t.cfg.Thresholds {
		if s >= th {
			level++
		}
	}
	return level
}

// secondsBelowLocked is the smallest whole number of idle seconds after
// which st reads below threshold, or 0 if it already does.
//
// With E = score - floor and d = threshold - 0.5 - floor, the score reads
// below the threshold once E * 2^(-s/halfLife) < d, so
// s = floor(halfLife * log2(E/d)) + 1. The result is then checked against
// the reading itself, one second either way, so float error in the log
// cannot disagree with what Read would report.
func (t *Throttle) secondsBelowLocked(st *keyState, threshold int) int {
	if t.whole(st) < threshold {
		return 0
	}
	e := st.score - float64(t.cfg.Floor)
	d := float64(threshold) - 0.5 - float64(t.cfg.Floor)
	hl := t.cfg.HalfLife.Seconds()
	below := func(s int) bool {
		return int(math.Round(float64(t.cfg.Floor)+e*math.Exp2(-float64(s)/hl))) < threshold
	}
	s := int(math.Floor(hl*math.Log2(e/d))) + 1
	if s < 1 {
		s = 1
	}
	for s > 1 && below(s-1) {
		s--
	}
	for !below(s) {
		s++
	}
	return s
}
