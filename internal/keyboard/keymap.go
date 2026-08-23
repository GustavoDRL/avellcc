package keyboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultMap8295 maps key names to (row, col) grid positions on the ITE 8295's
// 6x20 grid. It does NOT apply to the ITE 8291, whose grid is wired differently.
var DefaultMap8295 = map[string][2]int{
	// Row 0: Function row
	"esc":    {0, 0},
	"f1":     {0, 2},
	"f2":     {0, 3},
	"f3":     {0, 4},
	"f4":     {0, 5},
	"f5":     {0, 6},
	"f6":     {0, 7},
	"f7":     {0, 8},
	"f8":     {0, 9},
	"f9":     {0, 10},
	"f10":    {0, 11},
	"f11":    {0, 12},
	"f12":    {0, 13},
	"prtsc":  {0, 15},
	"scroll": {0, 16},
	"pause":  {0, 17},

	// Row 1: Number row
	"grave":     {1, 0},
	"1":         {1, 2},
	"2":         {1, 3},
	"3":         {1, 4},
	"4":         {1, 5},
	"5":         {1, 6},
	"6":         {1, 7},
	"7":         {1, 8},
	"8":         {1, 9},
	"9":         {1, 10},
	"0":         {1, 11},
	"minus":     {1, 12},
	"equal":     {1, 13},
	"backspace": {1, 14},
	"insert":    {1, 15},
	"home":      {1, 16},
	"pageup":    {1, 17},
	"numlock":   {1, 18},
	"num_slash": {1, 19},

	// Row 2: QWERTY row
	"tab":       {2, 0},
	"q":         {2, 2},
	"w":         {2, 3},
	"e":         {2, 4},
	"r":         {2, 5},
	"t":         {2, 6},
	"y":         {2, 7},
	"u":         {2, 8},
	"i":         {2, 9},
	"o":         {2, 10},
	"p":         {2, 11},
	"lbracket":  {2, 12},
	"rbracket":  {2, 13},
	"backslash": {2, 14},
	"delete":    {2, 15},
	"end":       {2, 16},
	"pagedown":  {2, 17},
	"num_7":     {2, 18},
	"num_8":     {2, 19},

	// Row 3: Home row
	"capslock":   {3, 0},
	"a":          {3, 2},
	"s":          {3, 3},
	"d":          {3, 4},
	"f":          {3, 5},
	"g":          {3, 6},
	"h":          {3, 7},
	"j":          {3, 8},
	"k":          {3, 9},
	"l":          {3, 10},
	"semicolon":  {3, 11},
	"apostrophe": {3, 12},
	"enter":      {3, 14},
	"num_4":      {3, 18},
	"num_5":      {3, 19},

	// Row 4: Shift row
	"lshift": {4, 0},
	"z":      {4, 3},
	"x":      {4, 4},
	"c":      {4, 5},
	"v":      {4, 6},
	"b":      {4, 7},
	"n":      {4, 8},
	"m":      {4, 9},
	"comma":  {4, 10},
	"period": {4, 11},
	"slash":  {4, 12},
	"rshift": {4, 14},
	"up":     {4, 16},
	"num_1":  {4, 18},
	"num_2":  {4, 19},

	// Row 5: Bottom row
	"lctrl":   {5, 0},
	"lmeta":   {5, 1},
	"lalt":    {5, 2},
	"space":   {5, 6},
	"ralt":    {5, 10},
	"rmeta":   {5, 11},
	"menu":    {5, 12},
	"rctrl":   {5, 14},
	"left":    {5, 15},
	"down":    {5, 16},
	"right":   {5, 17},
	"num_0":   {5, 18},
	"num_dot": {5, 19},
}

// Aliases maps common key name variants to canonical names.
var Aliases = map[string]string{
	"escape":       "esc",
	"printscreen":  "prtsc",
	"print_screen": "prtsc",
	"scrolllock":   "scroll",
	"scroll_lock":  "scroll",
	"backtick":     "grave",
	"tilde":        "grave",
	"-":            "minus",
	"=":            "equal",
	"bksp":         "backspace",
	"bs":           "backspace",
	"ins":          "insert",
	"pgup":         "pageup",
	"pgdn":         "pagedown",
	"del":          "delete",
	"[":            "lbracket",
	"]":            "rbracket",
	"\\":           "backslash",
	"caps":         "capslock",
	";":            "semicolon",
	"'":            "apostrophe",
	"return":       "enter",
	",":            "comma",
	".":            "period",
	"/":            "slash",
	"win":          "lmeta",
	"super":        "lmeta",
	"alt":          "lalt",
	"altgr":        "ralt",
	"ctrl":         "lctrl",
	"shift":        "lshift",
	"fn":           "lmeta",
}

// GetKeyPosition looks up the (row, col) grid position for a key name.
func GetKeyPosition(name string, keymap map[string][2]int) ([2]int, bool) {
	if keymap == nil {
		keymap = DefaultMap8295
	}
	n := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := Aliases[n]; ok {
		n = alias
	}
	pos, ok := keymap[n]
	return pos, ok
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "avellcc")
}

func keymapFile() string {
	return filepath.Join(configDir(), "keymap.json")
}

// ListKeys returns sorted list of all known key names.
func ListKeys(keymap map[string][2]int) []string {
	if keymap == nil {
		keymap = DefaultMap8295
	}
	keys := make([]string, 0, len(keymap))
	for k := range keymap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func copyMap(m map[string][2]int) map[string][2]int {
	c := make(map[string][2]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// DefaultMap8291 is the LED grid wiring of the Avell Storm 470, established by
// lighting positions and reading off which key came on. The grid's rows bear no
// relation to the keyboard's physical rows, and each row starts at its own
// column, so this cannot be derived from a layout — it had to be measured.
//
// Other ITE 8291 machines are wired differently: the controller exposes a bare
// 6x21 grid and the vendor decides what goes where. Run
// `avellcc keyboard calibrate` on those.
//
// The / key has no LED of its own on this machine, so it is absent here.
var DefaultMap8291 = map[string][2]int{
	// Grid row 0 — bottom row.
	"lctrl":     {0, 2},
	"fn":        {0, 3},
	"lmeta":     {0, 4},
	"lalt":      {0, 5},
	"space":     {0, 8},
	"ralt":      {0, 11},
	"rctrl":     {0, 13},
	"left":      {0, 14},
	"up":        {0, 15},
	"right":     {0, 16},
	"num_0":     {0, 17},
	"num_comma": {0, 18},
	"down":      {0, 19},

	// Grid row 1 — ZXCV row.
	"lshift":    {1, 2},
	"backslash": {1, 3},
	"z":         {1, 4},
	"x":         {1, 5},
	"c":         {1, 6},
	"v":         {1, 7},
	"b":         {1, 8},
	"n":         {1, 9},
	"m":         {1, 10},
	"comma":     {1, 11},
	"period":    {1, 12},
	"semicolon": {1, 13},
	"rshift":    {1, 15},
	"num_1":     {1, 16},
	"num_2":     {1, 17},
	"num_3":     {1, 18},
	"num_enter": {1, 19},

	// Grid row 2 — ASDF row.
	"capslock": {2, 2},
	"a":        {2, 3},
	"s":        {2, 4},
	"d":        {2, 5},
	"f":        {2, 6},
	"g":        {2, 7},
	"h":        {2, 8},
	"j":        {2, 9},
	"k":        {2, 10},
	"l":        {2, 11},
	"ç":        {2, 12},
	"~":        {2, 13},
	"rbracket": {2, 14},
	"num_4":    {2, 16},
	"num_5":    {2, 17},
	"num_6":    {2, 18},

	// Grid row 3 — QWERTY row.
	"tab":      {3, 2},
	"q":        {3, 3},
	"w":        {3, 4},
	"e":        {3, 5},
	"r":        {3, 6},
	"t":        {3, 7},
	"y":        {3, 8},
	"u":        {3, 9},
	"i":        {3, 10},
	"o":        {3, 11},
	"p":        {3, 12},
	"´":        {3, 13},
	"lbracket": {3, 14},
	"enter":    {3, 15},
	"num_7":    {3, 16},
	"num_8":    {3, 17},
	"num_9":    {3, 18},
	"num_plus": {3, 19},

	// Grid row 4 — number row.
	"apostrophe":   {4, 1},
	"1":            {4, 2},
	"2":            {4, 3},
	"3":            {4, 4},
	"4":            {4, 5},
	"5":            {4, 6},
	"6":            {4, 7},
	"7":            {4, 8},
	"8":            {4, 9},
	"9":            {4, 10},
	"0":            {4, 11},
	"minus":        {4, 12},
	"equal":        {4, 13},
	"backspace":    {4, 15},
	"numlock":      {4, 16},
	"num_slash":    {4, 17},
	"num_asterisk": {4, 18},
	"num_minus":    {4, 19},

	// Grid row 5 — function row.
	"esc":      {5, 1},
	"f1":       {5, 2},
	"f2":       {5, 3},
	"f3":       {5, 4},
	"f4":       {5, 5},
	"f5":       {5, 6},
	"f6":       {5, 7},
	"f7":       {5, 8},
	"f8":       {5, 9},
	"f9":       {5, 10},
	"f10":      {5, 11},
	"f11":      {5, 12},
	"f12":      {5, 13},
	"prtsc":    {5, 15},
	"delete":   {5, 16},
	"home":     {5, 17},
	"pageup":   {5, 18},
	"pagedown": {5, 19},
	"end":      {5, 20},
}

// specialKeyNames normalises the key names a terminal reports into the names
// used by the keymap.
//
// The kp* entries come from the extended keyboard protocol, which reports
// keypad keys separately from the main row. Terminals that do not negotiate it
// send a bare "7" for both, which calibration resolves on its own.
var specialKeyNames = map[string]string{
	"kp0":       "num_0",
	"kp1":       "num_1",
	"kp2":       "num_2",
	"kp3":       "num_3",
	"kp4":       "num_4",
	"kp5":       "num_5",
	"kp6":       "num_6",
	"kp7":       "num_7",
	"kp8":       "num_8",
	"kp9":       "num_9",
	"kpenter":   "num_enter",
	"kpplus":    "num_plus",
	"kpminus":   "num_minus",
	"kpmul":     "num_asterisk",
	"kpdiv":     "num_slash",
	"kpcomma":   "num_comma",
	"kpperiod":  "num_period",
	"kpequal":   "num_equal",
	"kpsep":     "num_comma",
	" ":         "space",
	"escape":    "esc",
	"pgup":      "pageup",
	"pgdown":    "pagedown",
	"pagedown":  "pagedown",
	"del":       "delete",
	"return":    "enter",
	"spacebar":  "space",
	"printscr":  "prtsc",
	"printscrn": "prtsc",
}

// CanonicalKeyName reduces a key name to the form used as a keymap key:
// lower-cased, modifier prefixes dropped, and aliases resolved.
func CanonicalKeyName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))

	// A shifted press still identifies the same physical key.
	for _, mod := range []string{"shift+", "ctrl+", "alt+", "super+", "meta+"} {
		n = strings.TrimPrefix(n, mod)
	}
	if n == "" {
		return ""
	}
	if mapped, ok := specialKeyNames[n]; ok {
		n = mapped
	}
	if alias, ok := Aliases[n]; ok {
		n = alias
	}
	return n
}

// keymapFileFor returns the on-disk keymap path for one controller. Keymaps are
// per-controller because the grids differ in both size and wiring.
func keymapFileFor(id string) string {
	return filepath.Join(configDir(), "keymap-"+id+".json")
}

// LoadKeymapFor loads the keymap for a controller: its own calibrated file
// first, then the legacy shared file (ITE 8295 only, which is what that file
// ever described), then the controller's built-in default.
func LoadKeymapFor(ctrl Controller) map[string][2]int {
	if m, ok := readKeymap(keymapFileFor(ctrl.KeymapID())); ok {
		return m
	}
	if ctrl.KeymapID() == KeymapIDITE8295 {
		if m, ok := readKeymap(keymapFile()); ok {
			return m
		}
	}
	return copyMap(ctrl.DefaultKeymap())
}

// SaveKeymapFor writes a calibrated keymap for one controller.
func SaveKeymapFor(ctrl Controller, keymap map[string][2]int) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(keymap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(keymapFileFor(ctrl.KeymapID()), data, 0o644)
}

// KeymapPathFor exposes where a controller's calibrated keymap is stored, for
// user-facing messages.
func KeymapPathFor(ctrl Controller) string {
	return keymapFileFor(ctrl.KeymapID())
}

func readKeymap(path string) (map[string][2]int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var raw map[string][2]int
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}
