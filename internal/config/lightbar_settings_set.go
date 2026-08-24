package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Writing a setting back is line-oriented rather than a re-encode of the
// struct. Marshalling would produce a valid file and throw away every comment
// in it — and the comments are most of what makes the file usable, since they
// carry the ranges and the reason each default is what it is. So the writer
// finds the one line that defines the key and rewrites only its value.

// settingField describes one settable key: where it lives and how to parse it.
type settingField struct {
	section string
	key     string
	kind    string // bool, int, float, string
	set     func(s *LightbarSettings, v any)
	get     func(s LightbarSettings) any
}

func settingFields() map[string]settingField {
	fields := []settingField{
		{"theme", "enabled", "bool",
			func(s *LightbarSettings, v any) { s.Theme.Enabled = v.(bool) },
			func(s LightbarSettings) any { return s.Theme.Enabled }},
		{"theme", "brightness", "int",
			func(s *LightbarSettings, v any) { s.Theme.Brightness = v.(int) },
			func(s LightbarSettings) any { return s.Theme.Brightness }},
		{"theme", "effect", "string",
			func(s *LightbarSettings, v any) { s.Theme.Effect = v.(string) },
			func(s LightbarSettings) any { return s.Theme.Effect }},
		{"theme", "speed", "int",
			func(s *LightbarSettings, v any) { s.Theme.Speed = v.(int) },
			func(s LightbarSettings) any { return s.Theme.Speed }},
		{"theme", "color_key", "string",
			func(s *LightbarSettings, v any) { s.Theme.ColorKey = v.(string) },
			func(s LightbarSettings) any { return s.Theme.ColorKey }},

		{"pulse", "enabled", "bool",
			func(s *LightbarSettings, v any) { s.Pulse.Enabled = v.(bool) },
			func(s LightbarSettings) any { return s.Pulse.Enabled }},
		{"pulse", "fps", "int",
			func(s *LightbarSettings, v any) { s.Pulse.FPS = v.(int) },
			func(s LightbarSettings) any { return s.Pulse.FPS }},
		{"pulse", "min_brightness", "int",
			func(s *LightbarSettings, v any) { s.Pulse.MinBrightness = v.(int) },
			func(s LightbarSettings) any { return s.Pulse.MinBrightness }},
		{"pulse", "max_brightness", "int",
			func(s *LightbarSettings, v any) { s.Pulse.MaxBrightness = v.(int) },
			func(s LightbarSettings) any { return s.Pulse.MaxBrightness }},
		{"pulse", "gain", "float",
			func(s *LightbarSettings, v any) { s.Pulse.Gain = v.(float64) },
			func(s LightbarSettings) any { return s.Pulse.Gain }},
		{"pulse", "player", "string",
			func(s *LightbarSettings, v any) { s.Pulse.Player = v.(string) },
			func(s LightbarSettings) any { return s.Pulse.Player }},
		{"pulse", "input_method", "string",
			func(s *LightbarSettings, v any) { s.Pulse.InputMethod = v.(string) },
			func(s LightbarSettings) any { return s.Pulse.InputMethod }},
		{"pulse", "input_source", "string",
			func(s *LightbarSettings, v any) { s.Pulse.InputSource = v.(string) },
			func(s LightbarSettings) any { return s.Pulse.InputSource }},

		// [keyboard] lives in the same file and was settable only by hand: the
		// list stopped at pulse.*, so `config set keyboard.brightness 3` came
		// back "unknown setting" for a key the file documents. One entry per
		// field of KeyboardSettings, and a test in this package fails if a
		// fourth field is ever added without a fourth entry here.
		{"keyboard", "enabled", "bool",
			func(s *LightbarSettings, v any) { s.Keyboard.Enabled = v.(bool) },
			func(s LightbarSettings) any { return s.Keyboard.Enabled }},
		{"keyboard", "brightness", "int",
			func(s *LightbarSettings, v any) { s.Keyboard.Brightness = v.(int) },
			func(s LightbarSettings) any { return s.Keyboard.Brightness }},
		{"keyboard", "color_key", "string",
			func(s *LightbarSettings, v any) { s.Keyboard.ColorKey = v.(string) },
			func(s LightbarSettings) any { return s.Keyboard.ColorKey }},
	}

	byName := make(map[string]settingField, len(fields))
	for _, f := range fields {
		byName[f.section+"."+f.key] = f
	}
	return byName
}

// SettingKeys lists every settable key, in stable order, for error messages
// and for anything that wants to enumerate the surface.
func SettingKeys() []string {
	fields := settingFields()
	keys := make([]string, 0, len(fields))
	for name := range fields {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// GetLightbarSetting returns one value as it would be written to the file.
func GetLightbarSetting(s LightbarSettings, name string) (string, error) {
	field, ok := settingFields()[name]
	if !ok {
		return "", unknownSettingKey(name)
	}
	return formatTOML(field.get(s)), nil
}

// SetLightbarSetting parses raw for the named key, applies it, and validates
// the whole settings object — a value that is fine on its own can still be
// wrong next to another, as min_brightness above max_brightness is.
func SetLightbarSetting(s *LightbarSettings, name, raw string) error {
	field, ok := settingFields()[name]
	if !ok {
		return unknownSettingKey(name)
	}

	var value any
	switch field.kind {
	case "bool":
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s takes true or false, got %q", name, raw)
		}
		value = b
	case "int":
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s takes a whole number, got %q", name, raw)
		}
		value = n
	case "float":
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return fmt.Errorf("%s takes a number, got %q", name, raw)
		}
		value = f
	default:
		value = strings.Trim(strings.TrimSpace(raw), `"`)
	}

	before := *s
	field.set(s, value)
	if err := s.Validate(); err != nil {
		*s = before
		return err
	}
	return nil
}

func unknownSettingKey(name string) error {
	return fmt.Errorf("unknown setting %q; the keys are %s",
		name, strings.Join(SettingKeys(), ", "))
}

// WriteLightbarSetting rewrites one key in the file in place, leaving every
// comment, blank line and unrelated setting exactly where it was.
//
// Three things make an in-place edit of a hand-editable file safe, and the
// first version of this had none of them:
//
//   - An exclusive lock. Two `config set` runs used to interleave their
//     read-modify-write; measured over 200 races, 29 left the file unloadable
//     and 78 silently dropped one of the two edits.
//   - An atomic replace. os.WriteFile truncates first, so a concurrent reader
//     could see a zero-byte file — which is valid TOML and loads as "every
//     default applies", with no error anywhere. A temp file plus rename means
//     a reader sees either the old file or the new one.
//   - Verification before committing. The line scanner is not a TOML parser
//     and never will be; instead of teaching it every construct, the candidate
//     text is decoded and compared against the settings this edit intended. A
//     scanner mistake — a header it failed to recognise, a key matched inside
//     a multi-line string — then becomes a refusal rather than a wrong write.
func WriteLightbarSetting(name, raw string) error {
	field, ok := settingFields()[name]
	if !ok {
		return unknownSettingKey(name)
	}

	unlock, err := lockSettings()
	if err != nil {
		return err
	}
	defer unlock()

	settings, err := LoadLightbarSettings()
	if err != nil {
		// The old code stopped here, which meant a file that failed to load
		// could not be repaired with the very command that exists to avoid
		// hand-editing it.
		return fmt.Errorf("%w\n\nthe file has to load before a single setting can be changed; "+
			"fix it by hand, or run `avellcc lightbar config reset` to start from the defaults "+
			"(the current file is kept as a .bak)", err)
	}

	expected := settings
	if err := SetLightbarSetting(&expected, name, raw); err != nil {
		return err
	}
	formatted := formatTOML(field.get(expected))

	if _, err := writeDefaultSettingsFileLocked(); err != nil {
		return err
	}

	path := LightbarSettingsPath()
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	candidate, err := replaceSettingLine(string(body), field.section, field.key, formatted)
	if err != nil {
		return err
	}

	// The keystone. Anything the scanner got wrong shows up here as a decode
	// error or as a settings object that is not the one asked for.
	got, err := DecodeLightbarSettings(candidate)
	if err != nil {
		return fmt.Errorf("refusing to write: editing %s would leave %s unparseable (%w) — "+
			"edit the file by hand", name, path, err)
	}
	if got != expected {
		return fmt.Errorf("refusing to write: editing %s in place would have changed other "+
			"settings too, so this file has a shape the editor cannot handle safely — "+
			"edit %s by hand", name, path)
	}

	return atomicWriteFile(path, []byte(candidate))
}

// ResetLightbarSettingsFile replaces the file with the commented defaults,
// keeping whatever was there as a .bak. It is the documented way out of a file
// that no longer loads, which is the one state `config set` cannot repair.
func ResetLightbarSettingsFile() (backup string, err error) {
	unlock, err := lockSettings()
	if err != nil {
		return "", err
	}
	defer unlock()

	path := LightbarSettingsPath()
	if body, err := os.ReadFile(path); err == nil {
		backup = path + ".bak"
		if err := atomicWriteFile(backup, body); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := atomicWriteFile(path, []byte(DefaultLightbarSettingsFile)); err != nil {
		return "", err
	}
	return backup, nil
}

// lockSettings takes an exclusive advisory lock for the whole read-modify-write.
// The lock lives in a sibling file because the settings file itself is replaced
// by rename, which would leave two writers holding locks on different inodes.
//
// Readers do not lock and do not need to: the rename means every read sees a
// complete file, either the old one or the new one.
func lockSettings() (func(), error) {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lightbar.toml.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("locking the settings file: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

// atomicWriteFile replaces path in one step. The temp file is created in the
// same directory so the rename cannot cross a filesystem, and it is fsynced
// before the rename so a crash cannot leave a renamed-but-empty file.
func atomicWriteFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".lightbar.toml.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// replaceSettingLine rewrites `key = value` inside `[section]`, appending the
// key — or the whole section — when the file does not have it yet.
//
// It is a scanner, not a parser, and the caller verifies its output; see
// WriteLightbarSetting. What it does handle, because the audit found each of
// these silently writing to the wrong key or appending a duplicate table:
// a comment after a section header, and spaces inside the brackets.
func replaceSettingLine(body, section, key, formatted string) (string, error) {
	var out []string
	current := ""
	replaced := false
	sectionEnd := -1

	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(stripComment(line))

		if name, ok := sectionHeader(trimmed); ok {
			current = name
		} else if current == section && !replaced && isAssignmentTo(trimmed, key) {
			// A trailing comment documents the setting, not the value it
			// happened to hold, so it survives the rewrite.
			if idx := commentIndex(line); idx >= 0 {
				line = fmt.Sprintf("%s = %s  %s", key, formatted, strings.TrimSpace(line[idx:]))
			} else {
				line = fmt.Sprintf("%s = %s", key, formatted)
			}
			replaced = true
		}

		out = append(out, line)
		if current == section {
			sectionEnd = len(out)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading %s: %w (a line longer than 1 MiB?)", LightbarSettingsPath(), err)
	}

	if replaced {
		return strings.Join(out, "\n") + "\n", nil
	}
	assignment := fmt.Sprintf("%s = %s", key, formatted)
	if sectionEnd < 0 {
		out = append(out, "", "["+section+"]", assignment)
		return strings.Join(out, "\n") + "\n", nil
	}
	// Land the new key at the end of its section rather than after the file's
	// last line, which would file it under whichever section happens to be last.
	out = append(out[:sectionEnd], append([]string{assignment}, out[sectionEnd:]...)...)
	return strings.Join(out, "\n") + "\n", nil
}

// sectionHeader recognises `[name]`, tolerating spaces inside the brackets.
// Missing those made `[ theme ]` and `[pulse]  # music` invisible, so the next
// write appended a duplicate table — or, worse, wrote the key into whichever
// section the scanner still thought it was in.
func sectionHeader(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", false
	}
	return strings.TrimSpace(trimmed[1 : len(trimmed)-1]), true
}

// commentIndex finds the `#` that starts a comment, ignoring one inside a
// quoted value — a colour like "#ff00ff" used to be re-emitted as a comment.
func commentIndex(line string) int {
	inQuote := false
	for i, r := range line {
		switch r {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return i
			}
		}
	}
	return -1
}

func stripComment(line string) string {
	if i := commentIndex(line); i >= 0 {
		return line[:i]
	}
	return line
}

// isAssignmentTo reports whether a line assigns to key. The line has already
// had any comment stripped.
func isAssignmentTo(trimmed, key string) bool {
	name, _, found := strings.Cut(trimmed, "=")
	return found && strings.TrimSpace(name) == key
}

func formatTOML(v any) string {
	switch val := v.(type) {
	case bool:
		return strconv.FormatBool(val)
	case int:
		return strconv.Itoa(val)
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	default:
		// Validate() has already rejected control characters and line breaks,
		// so the only escapes Go emits here are \" and \\, both of which are
		// TOML basic-string escapes.
		return strconv.Quote(fmt.Sprint(val))
	}
}
