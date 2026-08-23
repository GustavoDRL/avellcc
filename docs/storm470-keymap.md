# The LED grid and key map

The ITE 8291 exposes a bare 6×21 grid. Which physical key sits under which
position is decided by the laptop vendor, and the controller offers no way to
ask — there is no read-back of any kind. The map has to be **measured on the
machine**, and this document records both the result for the Avell Storm 470
and the method, because the first two attempts at it produced confident,
plausible and completely wrong maps.

## The Storm 470 map

Keyboard is **ABNT2** — it has `Ç`, `´`, `~`, and Caps Lock reads "Fixa".
`#` marks a numeric keypad key.

```
     0     1     2     3     4     5     6     7     8     9     10    11    12    13    14    15    16    17    18    19    20
r0   .     .     LCtl  fn    Super LAlt  .     .     Space .     .     AltGr .     RCtl  Left  Up    Right #0    #,    Down  .      <- bottom
r1   .     .     LShf  \     Z     X     C     V     B     N     M     ,     .     ;     .     RShf  #1    #2    #3    #Ent  .      <- ZXCV
r2   .     .     Caps  A     S     D     F     G     H     J     K     L     Ç     ~     ]     .     #4    #5    #6    .     .      <- ASDF
r3   .     .     Tab   Q     W     E     R     T     Y     U     I     O     P     ´     [     Enter #7    #8    #9    #+    .      <- QWERTY
r4   .     '     1     2     3     4     5     6     7     8     9     0     -     =     .     Bksp  NumLk #/    #*    #-    .      <- number
r5   .     Esc   F1    F2    F3    F4    F5    F6    F7    F8    F9    F10   F11   F12   .     PrtSc Del   Home  PgUp  PgDn  End    <- function
```

101 keys. Two structural facts that no layout knowledge would predict:

- **Grid rows do not follow physical rows.** Row 0 is the bottom row, row 5 is
  the function row, and the middle four are shuffled — ZXCV, ASDF, QWERTY,
  number. This ordering is what made a layout-driven calibration produce
  nonsense.
- **Each row starts at its own column.** The four letter rows begin at column 2;
  the number and function rows begin at column 1. Columns 0–1 belong to the wide
  keys that open each row (left Ctrl, left Shift, Caps, Tab), which span several
  columns and light as one unit.

Three individual surprises:

| Key | Where it actually is | Where it looks like it should be |
|---|---|---|
| **Enter** | grid row 3 (QWERTY), column 15 | the ASDF row, next to `]` |
| **Down arrow** | (0, 19) | (0, 15), between Left and Right |
| **`/`** | nowhere — it has no LED | (1, 14), after `;` |

The map lives in two places: `DefaultMap8291` in
`internal/keyboard/keymap.go`, so the fork works with no config file, and
`~/.config/avellcc/keymap-ite8291.json`, which wins when present.

## Key maps are per controller

| Controller | Grid | Built-in map | Calibrated file |
|---|---|---|---|
| ITE 8295 | 6×20 | `DefaultMap8295` | `~/.config/avellcc/keymap-ite8295.json` |
| ITE 8291 | 6×21 | `DefaultMap8291` (Storm 470) | `~/.config/avellcc/keymap-ite8291.json` |

The legacy shared `keymap.json` is still read, for the ITE 8295 only, since that
is the only layout it ever described. When no map resolves, `--key` fails with a
message pointing at `calibrate` rather than silently lighting the wrong key.

## Deriving the map on another machine

Other ITE 8291 laptops are wired differently. Two tools, and the order matters.

### 1. Establish the structure with `gridpaint`

`tools/gridpaint` lights grid positions directly, with no key map in the way.
Start by identifying what each grid row is:

```bash
go build -tags probe -o /tmp/gridpaint ./tools/gridpaint/
/tmp/gridpaint row=0:FF0000 row=1:00FF00 row=2:0000FF row=3:FFFF00 row=4:FF00FF row=5:00FFFF
```

Then find each row's starting column and the keypad block by lighting single
columns and single positions:

```bash
/tmp/gridpaint col=11:FFFFFF col=13:FF0000 col=15:00FF00 col=17:FF00FF
/tmp/gridpaint 0,15:FFFFFF 1,15:FF0000 2,15:00FF00 3,15:FF00FF
```

A single column lights at most one key per row, which is easy to read off
without counting.

### 2. Fill in the rest with `calibrate`

```bash
avellcc keyboard calibrate            # every position
avellcc keyboard calibrate --step 3   # anchors only, to interpolate between
```

One position lights bright white, the rest of its grid row glows dim blue so the
band being swept is visible. Press the key that is lit.

| Chord | Action |
|---|---|
| *(any key)* | record it at the lit position |
| `ctrl+s` | no key here — nothing lit up |
| `ctrl+n` | type a name, for keys the terminal cannot see (`Fn`, `Super`, modifiers) |
| `ctrl+f` | jump to the next unmapped position |
| `ctrl+g` | go to `row,col` or a position number |
| `ctrl+b` | back one position |
| `ctrl+d` | clear this position |
| `ctrl+q` | save and quit |

An existing map is loaded on start, so calibration can be resumed, and the saved
colours are restored on exit.

Keypad keys report as plain digits unless the terminal negotiates the extended
keyboard protocol. Calibration detects the collision and records the second
occurrence as `num_*` rather than overwriting the first; when the terminal
*does* report `kp7` and friends, they are mapped to `num_7` directly.

### 3. Verify before trusting it

Light the letters of a word and read it off the keyboard:

```bash
avellcc keyboard --profile verify.json   # keys: t,e,c,l,a,d,o all white
```

If the word is legible, the letter rows are right. Do the same for the arrows,
Enter, Backspace and the keypad, which are where the surprises live.

## What went wrong, and what it teaches

Three attempts were needed. Each failure had a distinct cause worth recording.

**A silent de-duplication bug.** The first calibration removed any earlier
position holding the same key name. Because the numeric keypad reports plain
`7` just like the number row, sweeping the keypad *deleted* the number row's
digits. The map came back missing 1–9, `-`, `,` and `/`. Fixed by resolving
collisions into `num_*` names instead of deleting.

**Sweeping in layout order rather than following the light.** With grid rows
shuffled, a sweep that assumes physical order yields a map that looks perfect —
`tab q w e r t y u i o p` in neat sequence, no gaps — and matches nothing. Real
grids have offsets and holes. **Tidy sequential rows are a red flag, not a good
sign.** The dim-blue row band exists to make the actual band visible so this
cannot happen unnoticed.

**Inferring the layout instead of measuring it.** Given the row identities, the
ABNT2 layout and a probe of column 11 that matched on all six rows, a full map
was generated by extrapolation. It was wrong: column 11 had matched by
coincidence, because every row happened to be correct at that one offset. A
single passing test says very little. The map that finally held up was checked
with keys spread across rows, columns and blocks.

The method that worked, in one line: **light a position and ask which key came
on** — never sweep and ask someone to press what they believe is lit. One is an
observation; the other is a guess wearing the costume of one.
