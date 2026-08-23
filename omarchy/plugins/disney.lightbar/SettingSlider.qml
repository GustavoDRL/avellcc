import QtQuick
import qs.Commons
import qs.Ui

// A labelled settings row built on the kit's PanelSlider: title, optional
// description, the live value on the right, and the track underneath.
//
// Three behaviours here are corrections of things an audit caught, and each is
// the reason a line looks the way it does.
//
// The knob follows `displayValue`, not the model. PanelSlider ends its own
// onReleased with `liveValue = value`; with `value` bound straight to the
// still-stale report, the knob jumped *backwards* on mouse-up and only caught
// up when the subprocess round-trip finished — while the number beside it
// already showed the new value. The two disagreed for the whole round trip,
// and permanently if the write failed.
//
// Commits are debounced. PanelSlider emits `released` on every wheel notch, so
// one trackpad flick used to queue dozens of serialized `config set` calls,
// each rewriting the settings file.
//
// And `awaitingWrite` keeps the shown value from being overwritten by a report
// that was already in flight when the user let go.
Item {
  id: root

  property QtObject bar: null
  property string label: ""
  property string description: ""
  property real value: 0
  property real minimum: 0
  property real maximum: 100
  property real step: 1
  property bool integer: false
  property int decimals: 0
  property string suffix: ""
  property bool hasCursor: false

  // How long to wait for a drag or a wheel burst to settle before writing.
  property int commitDelay: 180

  signal committed(real value)

  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property real displayValue: value
  property bool awaitingWrite: false

  onValueChanged: {
    if (track.dragging || root.awaitingWrite)
      return
    root.displayValue = root.value
  }

  // Called by the panel once a write has completed and the report has been
  // re-read, so the row goes back to following the model.
  function writesSettled() {
    root.awaitingWrite = false
    root.displayValue = root.value
  }

  function nudge(delta) {
    var next = Math.max(root.minimum, Math.min(root.maximum, root.displayValue + delta * root.step))
    root.displayValue = next
    commitTimer.restart()
  }

  implicitHeight: visible ? labels.implicitHeight + track.implicitHeight + Style.space(4) : 0
  height: implicitHeight

  Timer {
    id: commitTimer
    interval: root.commitDelay
    onTriggered: {
      root.awaitingWrite = true
      root.committed(root.integer ? Math.round(root.displayValue) : Number(root.displayValue.toFixed(root.decimals)))
    }
  }

  Item {
    id: labels
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    implicitHeight: Math.max(labelColumn.implicitHeight, valueText.implicitHeight)

    Column {
      id: labelColumn
      anchors.left: parent.left
      anchors.right: valueText.left
      anchors.rightMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      spacing: Style.space(1)

      Text {
        text: root.label
        color: root.hasCursor ? root.foreground : Qt.darker(root.foreground, 1.05)
        font.family: root.fontFamily
        font.pixelSize: Style.font.subtitle
        font.bold: root.hasCursor
        elide: Text.ElideRight
        width: parent.width
      }

      Text {
        visible: root.description !== ""
        text: root.description
        color: Qt.darker(root.foreground, 1.5)
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
        width: parent.width
      }
    }

    Text {
      id: valueText
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      text: (root.integer ? String(Math.round(root.displayValue)) : root.displayValue.toFixed(root.decimals)) + root.suffix
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.subtitle
      font.bold: true
    }
  }

  PanelSlider {
    id: track
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: labels.bottom
    anchors.topMargin: Style.space(2)
    bar: root.bar
    value: root.displayValue
    minimum: root.minimum
    maximum: root.maximum
    step: root.step
    integer: root.integer

    onMoved: function (v) {
      root.displayValue = v
    }
    onReleased: function (v) {
      root.displayValue = v
      commitTimer.restart()
    }
  }
}
