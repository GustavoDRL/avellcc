pragma ComponentBehavior: Bound

import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "Model.js" as Model

// Bar widget and panel for the Avell chassis light bar.
//
// The panel never touches ~/.config/avellcc/lightbar.toml itself. Editing TOML
// from QML would mean reimplementing the schema, the ranges and the comment
// handling here, in a second language, where nothing tests it — so every read
// is `avellcc lightbar config show --json` and every write is
// `avellcc lightbar config set <key> <value>`. Validation stays in one place,
// and a value this panel cannot produce is a value the CLI would have rejected
// anyway.
//
// The commands run by absolute path. The compositor's PATH is not the
// interactive shell's, and a command missing from it fails silently inside
// Quickshell — which looks exactly like a control that does nothing.
Panel {
  id: root
  moduleName: "disney.lightbar"
  ipcTarget: "disney.lightbar"

  readonly property string avellccBin: Quickshell.env("HOME") + "/.local/bin/avellcc"
  readonly property string systemctlBin: "/usr/bin/systemctl"
  readonly property string unitName: "avellcc-pulse.service"

  // The last report from the CLI, and the systemd unit's state, which the
  // report deliberately does not carry — avellcc knows nothing about units.
  property var report: ({})
  property string serviceState: ""
  property string lastError: ""
  property bool actionFailed: false

  readonly property var cfgTheme: (report.settings && report.settings.theme) || ({})
  readonly property var cfgPulse: (report.settings && report.settings.pulse) || ({})
  readonly property var barState: report.state || ({})
  readonly property bool serviceActive: serviceState === "active"
  readonly property string barMode: Model.barState(report, serviceActive)

  // Rebuilt only when the palette itself changes.
  property var bandModel: []
  readonly property var themePalette: root.report.palette || ({})
  onThemePaletteChanged: root.bandModel = Model.bands(root.report)

  readonly property color liveColor: Model.iconColor(report, root.bar ? root.bar.foreground : Color.foreground)

  // Sliders write on release, never while dragging: a drag at 60 Hz would
  // rewrite the settings file sixty times a second, and the daemon re-reads it
  // once a second, so the intermediate values would be both wasteful and
  // visible as stutter.
  function setSetting(key, value) {
    enqueue([root.avellccBin, "lightbar", "config", "set", key, String(value)])
    // Nothing re-applies the [theme] half on its own: those values are read by
    // `avellcc lightbar --theme` and by the omarchy theme hook, and the daemon
    // only ever reads [pulse]. Without this the whole TEMA section moved its
    // controls and left the hardware untouched until the user separately
    // pressed "Cor do tema" — which reads as the section being broken.
    if (String(key).indexOf("theme.") === 0)
      enqueue([root.avellccBin, "lightbar", "--theme"])
  }

  function runLightbar(args) {
    enqueue([root.avellccBin, "lightbar"].concat(args))
  }

  function restartService() {
    enqueue([root.systemctlBin, "--user", "restart", root.unitName])
  }

  // One command at a time, in order. Two `config set` calls racing would each
  // read the file, edit their own key and write the whole thing back.
  //
  // There is deliberately no shadow `running` flag. There used to be one, set
  // before starting the process and cleared in onExited — and Quickshell emits
  // only `runningChanged`, never `exited`, when a process fails to start. So a
  // missing or unexecutable binary left the flag stuck at true and every later
  // control in this panel became a silent no-op for the rest of the session.
  // The Process's own `running` is the single source of truth, which is what
  // the first-party panels use.
  property var queue: []

  function enqueue(command) {
    var next = queue.slice()
    next.push(command)
    queue = next
    pump()
  }

  function pump() {
    if (queue.length === 0 || actionProc.running) return
    var next = queue.slice()
    var command = next.shift()
    queue = next
    actionProc.command = command
    actionProc.running = true
  }

  // Called once a command has finished and the report has been re-read, so the
  // rows stop showing the value the user just chose and go back to following
  // the file.
  // The panel's keyboard cursor.
  //
  // PanelKeyCatcher accepts the arrows, j/k/h/l, Space and Enter *before* any
  // focused descendant sees them, and KeyboardPanel force-focuses it when the
  // panel opens. A panel that does not implement a cursor is therefore not
  // merely un-navigable — it actively swallows every key, so Enter never
  // reaches a toggle and Tab never reaches a control. Implementing the cursor
  // is the other half of using that pair.
  property int cursorIndex: 0
  property bool cursorActive: false

  // Rows in visual order. `speedRow` is only shown for an animated effect, so
  // the cursor has to skip it rather than land on an invisible row.
  readonly property var rows: [pulseToggle, minBrightnessRow, maxBrightnessRow, gainRow, fpsRow, themeToggle, themeBrightnessRow, effectDropdown, speedRow, colorKeyDropdown, themeButton, offButton, restartButton]

  function rowAt(index) {
    return index >= 0 && index < root.rows.length ? root.rows[index] : null
  }

  function moveCursor(delta) {
    if (!root.cursorActive) {
      root.cursorActive = true
      var current = root.rowAt(root.cursorIndex)
      if (current && current.visible)
        return
    }
    var next = root.cursorIndex
    for (var step = 0; step < root.rows.length; step++) {
      next = (next + delta + root.rows.length) % root.rows.length
      var row = root.rowAt(next)
      if (row && row.visible) {
        root.cursorIndex = next
        return
      }
    }
  }

  // Left and right adjust the row under the cursor when it holds a value, so a
  // slider is usable without reaching for the mouse.
  function adjustCursor(delta) {
    var row = root.rowAt(root.cursorIndex)
    if (!row || !row.visible)
      return
    if (typeof row.nudge === "function")
      row.nudge(delta)
    else
      root.moveCursor(delta)
  }

  function activateCursor() {
    var row = root.rowAt(root.cursorIndex)
    if (!row || !row.visible)
      return
    if (typeof row.toggle === "function")
      row.toggle()
    else if (typeof row.clicked === "function")
      row.clicked()
  }

  function writesSettled() {
    minBrightnessRow.writesSettled()
    maxBrightnessRow.writesSettled()
    gainRow.writesSettled()
    fpsRow.writesSettled()
    themeBrightnessRow.writesSettled()
    speedRow.writesSettled()
  }

  // The kit's Dropdown writes its own `value` when a row is picked, which
  // destroys any binding to it. Binding it to the report therefore worked
  // exactly once: after the first selection the control froze and stopped
  // tracking the file, including changes made from a terminal or by a theme
  // switch. So the panel assigns it explicitly instead.
  function syncDropdowns() {
    var effect = Model.str(root.cfgTheme.effect, "static")
    if (effectDropdown.value !== effect)
      effectDropdown.value = effect
    var key = Model.str(root.cfgTheme.color_key, "auto")
    if (colorKeyDropdown.value !== key)
      colorKeyDropdown.value = key
  }

  onReportChanged: root.syncDropdowns()

  function refresh() {
    if (!reportProc.running) reportProc.running = true
    if (!serviceProc.running) serviceProc.running = true
  }

  onOpenedChanged: {
    if (opened) {
      root.cursorActive = false
      root.cursorIndex = 0
      root.refresh()
    }
  }

  Component.onCompleted: refresh()

  Timer {
    // Only while the panel is open. A bar widget that shells out every few
    // seconds all day to paint one icon is not worth the wakeups; the icon
    // catches up the moment anyone looks at it.
    interval: Math.max(1, root.setting("refreshIntervalSec", 3)) * 1000
    running: root.opened
    repeat: true
    onTriggered: root.refresh()
  }

  Process {
    id: reportProc
    command: [root.avellccBin, "lightbar", "config", "show", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var parsed = JSON.parse(text)
          // JSON.parse("null") succeeds and yields null, after which every
          // binding that reaches into `report` throws.
          if (parsed === null || typeof parsed !== "object")
            throw new Error("not an object")
          root.report = parsed
          if (!root.actionFailed)
            root.lastError = root.report.error || ""
        } catch (e) {
          // A parse failure means the binary is missing or printed a
          // diagnostic; say so on the panel rather than rendering an empty
          // shell that looks like the feature is gone.
          root.lastError = "avellcc não respondeu como esperado"
        }
      }
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (String(text).trim() !== "") root.lastError = String(text).trim()
    }
  }

  Process {
    id: serviceProc
    command: [root.systemctlBin, "--user", "is-active", root.unitName]
    // `is-active` exits non-zero for every state that is not active, so the
    // exit code carries no information the output does not.
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.serviceState = String(text).trim()
    }
  }

  Process {
    id: actionProc
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (String(text).trim() !== "") root.lastError = String(text).trim()
    }
    onExited: function(exitCode) {
      root.running = false
      if (exitCode === 0) root.lastError = ""
      root.refresh()
      Qt.callLater(root.pump)
    }
  }

  // ---------------------------------------------------------------- bar icon

  // The bar measures a widget's slot from this item's implicit size
  // (Bar.qml: `implicitWidth: activeItem.implicitWidth`). `anchors.fill` on the
  // button does not propagate a size back up, so without these two lines the
  // slot is zero wide and the widget mounts, runs, and paints nothing — with no
  // QML error anywhere, because a zero-width item is perfectly legal.
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    tooltipText: "Light bar — " + Model.stateLabel(root.barMode, root.report.player)
    onPressed: function(b) {
      // Right click is the one-key escape hatch: back to the theme colour,
      // which is the state everything else returns to.
      if (b === Qt.RightButton) root.runLightbar(["--theme"])
      else root.toggle()
    }

    // A bar of light, painted in the colour the light bar is actually showing.
    // A glyph would say "light bar"; this says what it is doing.
    iconComponent: Component {
      Item {
        Rectangle {
          id: lightbarGlyph
          anchors.centerIn: parent
          width: Math.round(parent.width * 0.72)
          height: Math.max(3, Math.round(parent.height * 0.22))
          radius: height / 2
          color: root.liveColor
          opacity: Model.iconOpacity(root.report)

          Behavior on color { ColorAnimation { duration: 250 } }
          Behavior on opacity { NumberAnimation { duration: 250 } }

          // Pulsing is a state you should be able to read without opening
          // anything, so the icon breathes while the daemon owns the bar.
          SequentialAnimation on scale {
            running: root.barMode === Model.STATE_PULSE
            loops: Animation.Infinite
            NumberAnimation { to: 1.0; duration: 620; easing.type: Easing.InOutQuad }
            NumberAnimation { to: 0.82; duration: 620; easing.type: Easing.InOutQuad }
            onStopped: lightbarGlyph.scale = 1.0
          }
        }
      }
    }
  }

  // ------------------------------------------------------------------ panel

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380))
    contentHeight: panel.fittedContentHeight(column.implicitHeight)

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      // Without this the panel's catcher intercepts j/k/Enter while a dropdown
      // popup is open, so the popup cannot be driven from the keyboard.
      blocked: effectDropdown.popupOpen || colorKeyDropdown.popupOpen
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }

      Column {
        id: column
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        spacing: Style.space(12)

        // ---------- hero ----------
        Item {
          width: parent.width
          implicitHeight: Math.max(heroSwatch.implicitHeight, heroLabels.implicitHeight)

          Rectangle {
            id: heroSwatch
            width: Style.space(46)
            height: Style.space(10)
            radius: height / 2
            color: root.liveColor
            opacity: Model.iconOpacity(root.report)
            anchors.left: parent.left
            anchors.verticalCenter: parent.verticalCenter
            implicitHeight: Style.space(10)

            Behavior on color { ColorAnimation { duration: 250 } }
          }

          Column {
            id: heroLabels
            anchors.left: heroSwatch.right
            anchors.leftMargin: Style.space(14)
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            spacing: Style.space(2)

            Text {
              text: "Light bar"
              color: root.bar ? root.bar.foreground : Color.foreground
              font.family: root.bar ? root.bar.fontFamily : Style.font.family
              font.pixelSize: Style.font.title
              font.bold: true
              elide: Text.ElideRight
              width: parent.width
            }

            Text {
              text: Model.stateLabel(root.barMode, root.report.player).toUpperCase()
              color: Qt.darker(root.bar ? root.bar.foreground : Color.foreground, 1.4)
              font.family: root.bar ? root.bar.fontFamily : Style.font.family
              font.pixelSize: Style.font.caption
              font.bold: true
              font.letterSpacing: 1.2
              elide: Text.ElideRight
              width: parent.width
            }
          }
        }

        // ---------- the three band colours ----------
        Row {
          width: parent.width
          spacing: Style.space(8)
          visible: !!root.report.palette

          Repeater {
            // Bound to the palette rather than to `report`: `bands()` builds
            // fresh object literals, so modelling it on the whole report
            // destroyed and recreated all three delegates on every poll.
            model: root.bandModel
            delegate: Rectangle {
              id: bandCell
              required property var modelData
              width: (column.width - Style.space(16)) / 3
              height: Style.space(42)
              radius: Style.cornerRadius
              color: Qt.rgba(0, 0, 0, 0)
              border.width: 1
              border.color: Qt.rgba(root.bar.foreground.r, root.bar.foreground.g, root.bar.foreground.b, 0.15)

              Column {
                anchors.centerIn: parent
                spacing: Style.space(4)

                Rectangle {
                  width: Style.space(28)
                  height: Style.space(6)
                  radius: height / 2
                  color: bandCell.modelData.color || "transparent"
                  anchors.horizontalCenter: parent.horizontalCenter
                }

                Text {
                  text: bandCell.modelData.label
                  color: Qt.darker(root.bar.foreground, 1.4)
                  font.family: root.bar.fontFamily
                  font.pixelSize: Style.font.caption
                  anchors.horizontalCenter: parent.horizontalCenter
                }
              }

              // Clicking a band paints the bar that colour right now. This is
              // state, not settings: the next theme switch overwrites it, and
              // the footer note says so.
              MouseArea {
                anchors.fill: parent
                cursorShape: Qt.PointingHandCursor
                onClicked: if (bandCell.modelData.color) root.runLightbar(["--color", bandCell.modelData.color])
              }
            }
          }
        }

        PanelSeparator {
          width: parent.width
          foreground: root.bar.foreground
        }
        PanelSectionHeader {
          text: "PULSO"
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
        }

        Toggle {
          id: pulseToggle
          width: parent.width
          hasCursor: root.cursorActive && root.cursorIndex === 0
          accent: root.bar.accent !== undefined ? root.bar.accent : Color.accent
          label: "Pulsar com a música"
          description: root.serviceActive
            ? "Segue " + Model.shortPlayer(root.report.player)
            : "O serviço avellcc-pulse não está ativo"
          checked: Model.bool(root.cfgPulse.enabled, false)
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
          onClicked: root.setSetting("pulse.enabled", !Model.bool(root.cfgPulse.enabled, false))
        }

        SettingSlider {
          id: minBrightnessRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 1
          label: "Brilho entre batidas"
          value: Model.num(root.cfgPulse.min_brightness, 12)
          minimum: 0
          maximum: 100
          integer: true
          step: 1
          suffix: ""
          onCommitted: function(v) { root.setSetting("pulse.min_brightness", v) }
        }

        SettingSlider {
          id: maxBrightnessRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 2
          label: "Brilho no pico"
          value: Model.num(root.cfgPulse.max_brightness, 100)
          minimum: 0
          maximum: 100
          integer: true
          step: 1
          onCommitted: function(v) { root.setSetting("pulse.max_brightness", v) }
        }

        SettingSlider {
          id: gainRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 3
          label: "Ganho"
          description: "Sobe se o pico nunca encosta no teto"
          value: Model.num(root.cfgPulse.gain, 1.25)
          minimum: 0.5
          maximum: 3.0
          step: 0.05
          decimals: 2
          onCommitted: function(v) { root.setSetting("pulse.gain", v) }
        }

        SettingSlider {
          id: fpsRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 4
          label: "Taxa de quadros"
          value: Model.num(root.cfgPulse.fps, 30)
          minimum: 10
          maximum: 60
          integer: true
          step: 5
          suffix: " Hz"
          onCommitted: function(v) { root.setSetting("pulse.fps", v) }
        }

        PanelSeparator {
          width: parent.width
          foreground: root.bar.foreground
        }
        PanelSectionHeader {
          text: "TEMA"
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
        }

        Toggle {
          id: themeToggle
          width: parent.width
          hasCursor: root.cursorActive && root.cursorIndex === 5
          accent: root.bar.accent !== undefined ? root.bar.accent : Color.accent
          label: "Seguir o tema do Omarchy"
          description: "A cor de repouso, reescrita a cada troca de tema"
          checked: Model.bool(root.cfgTheme.enabled, true)
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
          onClicked: root.setSetting("theme.enabled", !Model.bool(root.cfgTheme.enabled, true))
        }

        SettingSlider {
          id: themeBrightnessRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 6
          label: "Brilho em repouso"
          value: Model.num(root.cfgTheme.brightness, 80)
          minimum: 0
          maximum: 100
          integer: true
          step: 1
          onCommitted: function(v) { root.setSetting("theme.brightness", v) }
        }

        Dropdown {
          id: effectDropdown
          width: parent.width
          hasCursor: root.cursorActive && root.cursorIndex === 7
          label: "Efeito"
          options: Model.effectOptions()
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
          onChanged: function(picked) {
            if (picked !== "" && picked !== Model.str(root.cfgTheme.effect, "static"))
              root.setSetting("theme.effect", picked)
          }
        }

        SettingSlider {
          id: speedRow
          width: parent.width
          bar: root.bar
          hasCursor: root.cursorActive && root.cursorIndex === 8
          label: "Velocidade"
          description: "1 é o mais rápido"
          visible: Model.effectAnimates(Model.str(root.cfgTheme.effect, "static"))
          value: Model.num(root.cfgTheme.speed, 5)
          minimum: 1
          maximum: 10
          integer: true
          step: 1
          onCommitted: function(v) { root.setSetting("theme.speed", v) }
        }

        Dropdown {
          id: colorKeyDropdown
          width: parent.width
          hasCursor: root.cursorActive && root.cursorIndex === 9
          label: "Cor do tema"
          options: Model.colorKeyOptions()
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
          onChanged: function(picked) {
            if (picked !== "" && picked !== Model.str(root.cfgTheme.color_key, "auto"))
              root.setSetting("theme.color_key", picked)
          }
        }

        PanelSeparator {
          width: parent.width
          foreground: root.bar.foreground
        }
        PanelSectionHeader {
          text: "AGORA"
          foreground: root.bar.foreground
          fontFamily: root.bar.fontFamily
        }

        // These write the bar, not the settings file. Saying so matters: a
        // person who turns the bar off here and then switches theme would
        // otherwise think the setting failed to stick.
        Row {
          width: parent.width
          spacing: Style.space(8)

          Button {
            id: themeButton
            hasCursor: root.cursorActive && root.cursorIndex === 10
            text: "Cor do tema"
            foreground: root.bar.foreground
            fontFamily: root.bar.fontFamily
            onClicked: root.runLightbar(["--theme"])
          }

          Button {
            id: offButton
            hasCursor: root.cursorActive && root.cursorIndex === 11
            text: root.barMode === Model.STATE_OFF ? "Acender" : "Apagar"
            foreground: root.bar.foreground
            fontFamily: root.bar.fontFamily
            onClicked: root.runLightbar(root.barMode === Model.STATE_OFF ? ["--theme"] : ["--off"])
          }

          Button {
            id: restartButton
            hasCursor: root.cursorActive && root.cursorIndex === 12
            text: "Reiniciar serviço"
            foreground: root.bar.foreground
            fontFamily: root.bar.fontFamily
            onClicked: root.restartService()
          }
        }

        Text {
          width: parent.width
          text: "Estes três agem na barra agora; a próxima troca de tema os desfaz."
          color: Qt.darker(root.bar.foreground, 1.5)
          font.family: root.bar.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }

        Text {
          width: parent.width
          visible: root.lastError !== ""
          text: root.lastError
          color: root.bar && root.bar.urgent ? root.bar.urgent : "#e06c75"
          font.family: root.bar.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }
      }
    }
  }

}
