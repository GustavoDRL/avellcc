package cmd

import (
	"strings"
	"testing"
)

// A recorded dbus-monitor session: Spotify starts playing, is paused, then
// quits. The interesting detail is the third block — a PropertiesChanged that
// carries Metadata but no PlaybackStatus must not be read as a status change.
const dbusMonitorSample = `signal time=1755900000.111 sender=:1.91 -> destination=(null destination) serial=88 path=/org/mpris/MediaPlayer2; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged
   string "org.mpris.MediaPlayer2.Player"
   array [
      dict entry(
         string "PlaybackStatus"
         variant             string "Playing"
      )
   ]
   array [
   ]
signal time=1755900030.222 sender=:1.91 -> destination=(null destination) serial=89 path=/org/mpris/MediaPlayer2; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged
   string "org.mpris.MediaPlayer2.Player"
   array [
      dict entry(
         string "PlaybackStatus"
         variant             string "Paused"
      )
   ]
   array [
   ]
signal time=1755900040.333 sender=:1.91 -> destination=(null destination) serial=90 path=/org/mpris/MediaPlayer2; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged
   string "org.mpris.MediaPlayer2.Player"
   array [
      dict entry(
         string "Metadata"
         variant             array [
            dict entry(
               string "xesam:title"
               variant                   string "PlaybackStatus"
            )
         ]
      )
   ]
   array [
   ]
signal time=1755900050.444 sender=org.freedesktop.DBus -> destination=:1.7 serial=91 path=/org/freedesktop/DBus; interface=org.freedesktop.DBus; member=NameOwnerChanged
   string "org.mpris.MediaPlayer2.spotify"
   string ":1.91"
   string ""
`

func TestScanPlayerSignalsReadsPlaybackStatus(t *testing.T) {
	var statuses []string
	ownerChanges := 0

	err := scanPlayerSignals(strings.NewReader(dbusMonitorSample),
		func(s string) { statuses = append(statuses, s) },
		func() { ownerChanges++ })
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Playing", "Paused"}
	if len(statuses) != len(want) {
		t.Fatalf("got %d status changes %v, want %v", len(statuses), statuses, want)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Errorf("status %d = %q, want %q", i, statuses[i], want[i])
		}
	}
	if ownerChanges != 1 {
		t.Errorf("got %d owner changes, want 1", ownerChanges)
	}
}

// A track whose title happens to be the string "PlaybackStatus" must not be
// mistaken for the property: the property name and its value are adjacent
// lines, and the value line always starts with "variant".
func TestScanPlayerSignalsIgnoresAMatchingTrackTitle(t *testing.T) {
	var statuses []string
	metadataOnly := dbusMonitorSample[strings.Index(dbusMonitorSample, "signal time=1755900040"):]

	if err := scanPlayerSignals(strings.NewReader(metadataOnly),
		func(s string) { statuses = append(statuses, s) }, func() {}); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 0 {
		t.Errorf("a Metadata-only signal produced status changes %v", statuses)
	}
}

func TestLastQuoted(t *testing.T) {
	cases := map[string]string{
		`variant             string "Playing"`: "Playing",
		`s "Paused"`:                           "Paused",
		`   string "PlaybackStatus"`:           "PlaybackStatus",
		`no quotes here`:                       "",
		`"`:                                    "",
		``:                                     "",
	}
	for in, want := range cases {
		if got := lastQuoted(in); got != want {
			t.Errorf("lastQuoted(%q) = %q, want %q", in, got, want)
		}
	}
}

// Omarchy Spotify's backend publishes a PID-qualified name, so watching the
// bare one would never see a signal. The bare name still wins whenever a
// player owns it, which is what Spotify's own client does.
func TestPickPlayerDest(t *testing.T) {
	const base = "org.mpris.MediaPlayer2.OmarchySpotify"
	names := []string{
		":1.42",
		"org.mpris.MediaPlayer2.spotify",
		"org.mpris.MediaPlayer2.OmarchySpotify.instance34177",
	}

	if got := pickPlayerDest(base, names); got != "org.mpris.MediaPlayer2.OmarchySpotify.instance34177" {
		t.Errorf("instance name not picked: %q", got)
	}
	if got := pickPlayerDest("org.mpris.MediaPlayer2.spotify", names); got != "org.mpris.MediaPlayer2.spotify" {
		t.Errorf("owned bare name not preferred: %q", got)
	}
	// The bare name is owned as well as an instance: the exact match wins so
	// a player that owns both is watched on its stable name.
	if got := pickPlayerDest(base, append(names, base)); got != base {
		t.Errorf("bare name lost to an instance: %q", got)
	}
	// Nothing playing yet. Returning base keeps the match rule valid, and the
	// bus resolves it once the player appears.
	if got := pickPlayerDest(base, nil); got != base {
		t.Errorf("absent player = %q, want the bare name", got)
	}
	// A sibling namespace must not be mistaken for an instance of this one.
	if got := pickPlayerDest("org.mpris.MediaPlayer2.vlc",
		[]string{"org.mpris.MediaPlayer2.vlcsomething"}); got != "org.mpris.MediaPlayer2.vlc" {
		t.Errorf("prefix leaked across names: %q", got)
	}
}

func TestParseBusNames(t *testing.T) {
	const out = `as 4 "org.freedesktop.DBus" ":1.0" "org.mpris.MediaPlayer2.OmarchySpotify.instance7" ":1.91"` + "\n"

	got := parseBusNames(out)
	want := []string{"org.freedesktop.DBus", ":1.0", "org.mpris.MediaPlayer2.OmarchySpotify.instance7", ":1.91"}
	if len(got) != len(want) {
		t.Fatalf("parseBusNames returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
	if n := parseBusNames("as 0"); len(n) != 0 {
		t.Errorf("an empty reply produced %v", n)
	}
}
