# Per-key load-score throttle (design reference)

This is the frequency-capping stage of the dispatcher. It sits after the
preference check and before a message goes onto a channel queue. Each
recipient key (a user ID, or a user ID plus a channel) has a load score.
Every dispatch adds to the score, idle time lets it decay, and the score's
band decides which messages may still be sent.

`excerpt.go` holds the original single-score version. It has the build tag
`//go:build ignore`, so it is never compiled. Use it only as a reference for
the arithmetic. The rest of this page describes what the real package should
do differently.

## The model

- **Score.** A float per key. It starts at the floor, rises by a fixed cost
  per event, and is limited to the range `[floor, cap]`.
- **Decay.** Only the excess above the floor decays. It halves every
  `halfLife` of idle time:
  `score = floor + (score - floor) * 2^(-dt / halfLife)`.
  Decay is applied lazily whenever the key is read or written, so no timers
  are needed.
- **Bands.** A single `levelOf(score)` function maps the score to
  `LOW` / `MEDIUM` / `HIGH` / `CRITICAL`. Nothing else holds a copy of the
  thresholds.
- **Costs.** Each send attempt costs a fixed amount. A refusal or delivery
  fault costs more. A clean delivery acknowledgement gives a little back.

The excerpt's numbers are arbitrary units: floor 54, cap 104, band thresholds
68 / 82 / 96 and a half-life of 40 s. Moved to a floor of 0, they become
thresholds 14 / 28 / 42 and a cap of 50. A per-recipient notification
throttle wants a much longer half-life, in the range of 10 to 60 minutes.

## Changes from the excerpt

1. **One score per key, not one global.** Replace the package-level `state`
   with a struct held in a mutex-guarded map:

   ```go
   type keyState struct {
       score   float64
       settled time.Time
       init    bool
   }

   type Throttle struct {
       mu    sync.Mutex
       keys  map[string]*keyState
       clock func() time.Time
       cfg   Config
   }
   ```

   One mutex over the map is enough at this scale. Remove keys whose score
   has decayed back to the floor, either during a periodic sweep or when
   they are next read, so the map stays bounded.

2. **Explicit init flag.** The excerpt treats `score == 0` as "never
   touched". That breaks as soon as the floor is 0, because a valid score
   then looks uninitialised. Use `keyState.init` instead.

3. **Suppress sends by band, not by vent.** The excerpt banks overflow above
   the cap and, once the bank is full, "sheds": it drops the score and
   silences the whole next turn. Drop that mechanism. Instead, the band of
   the key decides which messages go out:

   | Band       | Sends                                      | Everything else         |
   |------------|--------------------------------------------|-------------------------|
   | `LOW`      | all                                        | -                       |
   | `MEDIUM`   | transactional and higher                   | marketing is batched    |
   | `HIGH`     | high priority and critical                 | deferred to a digest    |
   | `CRITICAL` | critical only (2FA, password reset)        | deferred                |

   Critical messages always bypass the throttle, as the routing rules in
   `idea.txt` require. The overflow count can stay as a metric for the
   dashboard, but it no longer gates anything.

4. **A turn is one dispatch attempt or one batch.** In the excerpt, a turn
   was an interactive step. Here, call `Allow(key, priority)` before each
   attempt or batch, and `Record(key, outcome)` after it. The outcome is
   sent, refused, faulted or acknowledged, and it applies the matching cost.

5. **Closed-form forecast.** The excerpt finds the time to the next band by
   stepping forward one second at a time, up to 600 steps. Solve it
   directly instead. With `E = score - floor`, current level `L >= 1` and
   that level's threshold `T_L`, the score reads one band lower once
   `round(floor + E * 2^(-s / halfLife)) < T_L`. The smallest whole number
   of seconds that satisfies this is:

   ```
   d = T_L - 0.5 - floor
   s = floor(halfLife_seconds * log2(E / d)) + 1
   ```

   If `s` is larger than the horizon, report no forecast. At `LOW` there is
   no lower band. This formula was checked against the excerpt's loop on
   2,665 sampled scores, with no mismatches. The API can use it for a
   `Retry-After` value on deferred messages.

6. **Injected clock.** Keep the excerpt's `clock` variable, but make it a
   field on `Throttle` so that tests can hold time still or step it
   forward.

## Not covered here

- Persistence. The scores are in-memory. After a restart every key reads as
  `LOW`, which errs toward sending.
- Delivery logs. Recipient text should go through the shared text sanitizer
  from the CRUD project before it is logged.
