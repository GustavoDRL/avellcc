package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hugo-andrade/avellcc/internal/config"
	"github.com/hugo-andrade/avellcc/internal/lightbar"
	"github.com/hugo-andrade/avellcc/internal/omarchy"
)

// `avellcc lightbar config` is the surface the bar plugin drives. A panel
// cannot edit a TOML file safely — it would have to reimplement the schema,
// the ranges and the comment handling in QML — so it calls this instead, and
// the validation stays in one place.

var lbConfigJSON bool

var lightbarConfigCmd = &cobra.Command{
	Use:           "config",
	Short:         "Read and write the light bar settings file",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var lightbarConfigShowCmd = &cobra.Command{
	Use:           "show",
	Short:         "Print the settings in force",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if lbConfigJSON {
			return printLightbarConfigJSON()
		}
		return showLightbarConfig(cmd)
	},
}

var lightbarConfigGetCmd = &cobra.Command{
	Use:           "get <key>",
	Short:         "Print one setting",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, args []string) error {
		settings, err := config.LoadLightbarSettings()
		if err != nil {
			return err
		}
		value, err := config.GetLightbarSetting(settings, args[0])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	},
}

var lightbarConfigSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Change one setting, keeping the file's comments",
	Long: "Change one setting in ~/.config/avellcc/lightbar.toml.\n\n" +
		"The value is validated before anything is written, and the file's\n" +
		"comments and layout are preserved. The pulse daemon picks the change\n" +
		"up within about a second; only pulse.player needs a service restart.",
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, args []string) error {
		if err := config.WriteLightbarSetting(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("%s = %s\n", args[0], args[1])
		return nil
	},
}

// A file that no longer loads is the one state `config set` cannot repair,
// because it validates by loading first. This is the documented way out, and
// the error from `config set` names it.
var lightbarConfigResetCmd = &cobra.Command{
	Use:           "reset",
	Short:         "Replace the settings file with the commented defaults, keeping a .bak",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		backup, err := config.ResetLightbarSettingsFile()
		if err != nil {
			return err
		}
		if backup != "" {
			fmt.Printf("Previous file kept at %s\n", backup)
		}
		fmt.Printf("%s reset to the defaults.\n", config.LightbarSettingsPath())
		return nil
	},
}

var lightbarConfigPathCmd = &cobra.Command{
	Use:           "path",
	Short:         "Print the settings file path",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		fmt.Println(config.LightbarSettingsPath())
		return nil
	},
}

var lightbarConfigKeysCmd = &cobra.Command{
	Use:           "keys",
	Short:         "List every settable key",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		for _, key := range config.SettingKeys() {
			fmt.Println(key)
		}
		return nil
	},
}

func init() {
	lightbarConfigShowCmd.Flags().BoolVar(&lbConfigJSON, "json", false,
		"Emit JSON, for the bar plugin and other machine consumers")
	lightbarConfigCmd.AddCommand(
		lightbarConfigShowCmd,
		lightbarConfigGetCmd,
		lightbarConfigSetCmd,
		lightbarConfigPathCmd,
		lightbarConfigKeysCmd,
		lightbarConfigResetCmd,
	)
	lightbarCmd.AddCommand(lightbarConfigCmd)
}

// lightbarConfigReport is the shape the bar plugin consumes. It deliberately
// carries the derived values too — the palette the current theme resolves to,
// and what the bar was last told to show — because those are the things a
// panel would otherwise have to recompute from the theme files itself.
type lightbarConfigReport struct {
	Path     string                   `json:"path"`
	Exists   bool                     `json:"exists"`
	Settings *config.LightbarSettings `json:"settings,omitempty"`
	InForce  bool                     `json:"in_force"`
	Player   string                   `json:"player"`
	Palette  *paletteReport           `json:"palette,omitempty"`
	State    map[string]any           `json:"state,omitempty"`
	Device   string                   `json:"device,omitempty"`
	Theme    string                   `json:"theme,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

type paletteReport struct {
	Bass   string `json:"bass"`
	Mid    string `json:"mid"`
	Treble string `json:"treble"`
}

// printLightbarConfigJSON never fails on a missing theme or a missing device:
// a panel that gets no JSON has nothing to show, whereas a report with a field
// absent can still render the rest.
func printLightbarConfigJSON() error {
	report := lightbarConfigReport{Path: config.LightbarSettingsPath()}

	if _, err := os.Stat(report.Path); err == nil {
		report.Exists = true
	}

	settings, err := config.LoadLightbarSettings()
	if err != nil {
		// LoadLightbarSettings hands back the defaults on error, which are in
		// force nowhere: neither the hook nor the daemon uses them when the
		// file is broken. Publishing them as `settings` had the panel render
		// values that exist nowhere, so the field is omitted instead and
		// `in_force` says why.
		report.Error = err.Error()
		report.InForce = false
	} else {
		s := settings
		report.Settings = &s
		report.InForce = true
	}
	report.Player = settings.Pulse.MPRISName()

	if palette, err := omarchy.CurrentPalette(); err == nil {
		report.Palette = &paletteReport{
			Bass:   palette.Bass.Hex(),
			Mid:    palette.Mid.Hex(),
			Treble: palette.Treble.Hex(),
		}
	}
	if name, err := os.ReadFile(omarchy.ThemeNamePath()); err == nil {
		report.Theme = trimLine(string(name))
	}
	if path, _, err := lightbar.FindITE8233(); err == nil {
		report.Device = path
	}
	if state := loadLightbar8233State(); len(state) > 1 {
		report.State = state
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
