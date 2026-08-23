// Pure helpers for the light bar panel. Everything here is a function of the
// JSON `avellcc lightbar config show --json` prints, so the QML stays layout
// and this stays testable by reading it.

.pragma library

// STATE_* are the three things the bar can be doing, which is what both the
// icon and the hero line have to say.
var STATE_OFF = "off"
var STATE_THEME = "theme"
var STATE_PULSE = "pulse"

// barState folds the report and the systemd unit state into one answer.
// `playing` is not in the report — the daemon knows it but does not publish
// it — so the panel says "armed" rather than claiming the music is on.
function barState(report, serviceActive) {
    var settings = report.settings || {}
    var pulse = settings.pulse || {}
    var state = report.state || {}

    if (state.mode === "off")
        return STATE_OFF
    if (serviceActive && pulse.enabled)
        return STATE_PULSE
    return STATE_THEME
}

function stateLabel(state, playerName) {
    switch (state) {
    case STATE_OFF:
        return "Apagada"
    case STATE_PULSE:
        return "Seguindo " + shortPlayer(playerName)
    default:
        return "Cor do tema"
    }
}

// The bus name is what the daemon needs; a person wants the last segment.
function shortPlayer(name) {
    if (!name)
        return "o player"
    var parts = String(name).split(".")
    return parts[parts.length - 1]
}

// The icon paints in the colour the bar was last told to show, which is the
// most useful thing a glance at the status bar can give. Falls back to the
// theme's mid colour, then to the bar's own foreground.
function iconColor(report, fallback) {
    var state = report.state || {}
    if (state.mode !== "off" && typeof state.color === "string" && state.color.length === 7)
        return state.color
    var palette = report.palette || {}
    if (typeof palette.mid === "string" && palette.mid.length === 7)
        return palette.mid
    return fallback
}

// Brightness drives the icon's opacity, floored so an unlit bar is still
// visible as a widget rather than looking like a rendering bug.
function iconOpacity(report) {
    var state = report.state || {}
    if (state.mode === "off")
        return 0.25
    var brightness = Number(state.brightness)
    if (!isFinite(brightness))
        return 1.0
    return Math.max(0.35, Math.min(1.0, brightness / 100))
}

// The palette entry each frequency band paints with, for the swatch row.
function bands(report) {
    var palette = report.palette || {}
    return [
        { key: "bass", label: "grave", color: palette.bass || "" },
        { key: "mid", label: "médio", color: palette.mid || "" },
        { key: "treble", label: "agudo", color: palette.treble || "" }
    ]
}

// The effects the ITE 8233 has. Kept here rather than read from the CLI: it is
// a hardware fact that changes only when the driver does, and a dropdown that
// waits for a subprocess to populate reads as broken.
function effectOptions() {
    return [
        { value: "static", label: "Estática" },
        { value: "breathing", label: "Respiração" },
        { value: "wave", label: "Onda" },
        { value: "bounce", label: "Vaivém" },
        { value: "marquee", label: "Letreiro" },
        { value: "scan", label: "Varredura" }
    ]
}

// "auto" plus the palette keys the picker considers. Anything in the theme's
// colors.toml is valid, but offering all of them would be a list of forty.
function colorKeyOptions() {
    var keys = ["auto", "accent", "red", "yellow", "green", "cyan", "blue", "magenta",
                "bright_red", "bright_yellow", "bright_green", "bright_cyan",
                "bright_blue", "bright_magenta", "foreground"]
    var options = []
    for (var i = 0; i < keys.length; i++)
        options.push({ value: keys[i], label: keys[i] === "auto" ? "auto (contraste)" : keys[i] })
    return options
}

// Speed only means something to an effect that animates, so the row is hidden
// rather than shown doing nothing.
function effectAnimates(effect) {
    return effect !== "" && effect !== "static"
}

function num(value, fallback) {
    var n = Number(value)
    return isFinite(n) ? n : fallback
}

function bool(value, fallback) {
    return typeof value === "boolean" ? value : fallback
}

function str(value, fallback) {
    return typeof value === "string" && value !== "" ? value : fallback
}
