# Provenance

This file records where each adapted file came from. Sources are identified
by their SHA-256 and line ranges in the staged copies listed in
`D:\Portfolio\_extracted\MANIFEST.tsv`. Look a hash up there to find the
original path and symbol names. They are not repeated here because they
contain terms that are excluded from this project.

Line numbers are only valid for the recorded hash.

## Sources

| Label | Kind        | SHA-256 (staged copy)                                              | Lines used |
|-------|-------------|--------------------------------------------------------------------|------------|
| A     | Go source   | `D5E8A80050233D8D9F3A30D88C964D79D7A9F769D23E3DE8781356BF3550F914` | 54-173 (to end); comment block 9-52 dropped |
| B     | Go source   | `E749F1D4226A47865BDBA9F17851231EC7F3FD80DF0F7579A56D328E270E5949` | 111-210 |
| C     | Go source   | `F839DF7AC056FA4FC15FE45CE5B57F3BCCDC2833C5E1D0D795528185A1261E2D` | 88-97 |
| D     | TSV data    | `F71708D4A9562ED527951A0AFC948C39D08540FBC88C18E9AB7067B2A4EDD4E3` (`seasons.tsv`) | 41 (window dates), 44-49 (`@` rows) |

## reference/throttle/excerpt.go

This is a reference excerpt with the build tag `//go:build ignore`, so it is
never built. Identifiers were renamed and comments rewritten. The logic is
unchanged: `go vet` on the file passes.

| Source lines | Becomes |
|--------------|---------|
| C 88-97   | `bandLow`, `bandMedium`, `bandHigh`, `bandCritical` (were COLD, HEARTH, WARM, FURNACE); `pipFull`, `pipEmpty` |
| B 111-123 | `scoreFloor`, `scoreCap`, `costPerEvent`, `costRefused`, `costMinorFault`, `costMajorFault`, `creditCleanAck` |
| B 125-139 | `state` (fields `score`, `settled`, `overflow`, `shedDue`) |
| B 141-149 | `current` |
| B 151-156 | `readLocked` |
| B 158-179 | `add` |
| B 181-190 | `reset` |
| B 192-195 | `currentLevel` |
| B 197-210 | `levelOf` |
| A 54-60   | `halfLife`, `overflowCap`, `shedFloor`, `forecastHorizon`, `meterWidth` |
| A 62-64   | `clock` |
| A 66-82   | `settleLocked` |
| A 84-93   | `bands`, `bandOf` |
| A 95-111  | `reading`, `read` |
| A 113-135 | `forecast` |
| A 137-151 | `closeTurn` |
| A 153-161 | `openTurn` |
| A 163-172 | `overflowMeter` |

## reference/throttle/README.md

This file was written new. It describes the per-key design and lists the
changes to make from the excerpt. The closed-form forecast in it was checked
numerically against the loop in source A, lines 113-135.

## textsafe/

A Go port of the CRUD hub project's sanitizer. These are ACME's own files,
so they are not staged and are identified by hash only:

| File | Source | SHA-256 |
|------|--------|---------|
| `textsafe/textsafe.go`      | `textsafe.py`      | `D89AC226CB46C7FA182E5ABD88D87ECA4D793B758DE3872D619C57F7F10F6F48` |
| `textsafe/textsafe_test.go` | `test_textsafe.py` | `E611A2C4D4441DAF7E856D2ED5421B9B36047ADEA6FCE1ECC0F672DD37E27CC4` |

- `LIMIT`, `refused` and `clean` became `Limit`, `Refused` and `Clean`/`CleanN`.
  The rules and the 240-character cap are unchanged.
- Python's `limit=None` (no cap) became `Strip`. `None` text has no Go
  equivalent, so that test became an empty-string test.
- New tests: the cap counts characters rather than bytes, and invalid UTF-8
  becomes U+FFFD.

## throttle/

Written new, following `reference/throttle/README.md`. The decay arithmetic
is reimplemented from the excerpt (source A, `settleLocked`) for one score per
key. Its tests port the excerpt's forecast loop to check
the closed form against it.

## templates/seasonal.tsv

Taken from source D:

- The six `@` scene rows (lines 44-49) became template rows. The kind and
  source columns were dropped.
- The window name `HALLOWS` was renamed to `AUTUMN`.
- The window dates `10-01` to `11-02` come from line 41.
- One phrase changed in the first row: a scrubbed noun was replaced with
  "by the entrance". The other five rows are verbatim.
- The header comment was written new.
