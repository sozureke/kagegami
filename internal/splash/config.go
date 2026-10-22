// Package splash plays an animated ASCII splash in a glitch style: the art appears in rings that
// spread from a centre, pulses run over it, it glitches for a while and then dissolves. Every knob
// of the animation is a field of Config.
package splash

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config holds every parameter of the animation. The comment of each field says what it does and
// which way to turn it; the defaults are in DefaultConfig.
type Config struct {
	// Speed multiplies the time of the appearance and of the pulses. Higher is faster: 2.5 is
	// brisk, 1 is calm. The seconds of the hold and of the fade-out are not affected.
	Speed float64 `yaml:"speed"`
	// FPS is the frames per second. Higher is smoother and costs more CPU while it plays; 30 is
	// enough.
	FPS int `yaml:"fps"`

	// Rings is the number of rings in which the art appears, from the centre outwards. More rings
	// make a finer ripple and a longer appearance; 1 makes the art appear in one sweep.
	Rings int `yaml:"rings"`
	// Pulse is the pause between two rings, in frames. Larger spreads the rings out; 0 makes
	// them appear together.
	Pulse float64 `yaml:"pulse"`
	// Fade is how many frames a single character takes to appear. Larger is a softer emergence;
	// 1 is a hard cut.
	Fade float64 `yaml:"fade"`
	// Jitter is a random spread of the start of each character, in frames. Larger makes the edge of
	// a ring ragged; 0 makes it a clean line.
	Jitter float64 `yaml:"jitter"`

	// CenterX and CenterY are the centre of the rings and of the pulses, as a percentage of the
	// size of the art: 50, 50 is the middle, 0, 0 the top left corner.
	CenterX float64 `yaml:"center_x"`
	CenterY float64 `yaml:"center_y"`

	// PulseFreq is how often a pulse starts after the art has appeared, from 0 (never) to 100.
	// Higher is more pulses.
	PulseFreq float64 `yaml:"pulse_freq"`
	// PulseSpeed is how fast a pulse travels. Higher is faster.
	PulseSpeed float64 `yaml:"pulse_speed"`
	// PulseWidth is the thickness of a pulse, in tenths of a cell. Higher is a thicker ring.
	PulseWidth float64 `yaml:"pulse_width"`

	// Glitch is the amount of glitching, from 0 (none at all) to 100: bursts that shift whole blocks
	// of rows, single shifted rows, noise, rebuilt patches and flicker. Higher is wilder.
	Glitch float64 `yaml:"glitch"`
	// RGBSplit is how often a glitching row shows a red and a cyan ghost beside its characters,
	// from 0 (never) to 100.
	RGBSplit float64 `yaml:"rgb_split"`

	// Scanlines darkens every second row.
	Scanlines bool `yaml:"scanlines"`
	// Vignette darkens the art towards its edges.
	Vignette bool `yaml:"vignette"`
	// CellAspect is the width of a terminal cell divided by its height, about 0.5. Raise it if the
	// rings look like ovals standing up, lower it if they look flat.
	CellAspect float64 `yaml:"cell_aspect"`

	// FadeOutSec is how long the art takes to dissolve at the end, in seconds. Larger is slower.
	FadeOutSec float64 `yaml:"fade_out_sec"`
	// HoldSec is how long the finished art glitches and pulses before it dissolves, in seconds.
	HoldSec float64 `yaml:"hold_sec"`
	// TotalMaxSec is the longest the whole animation may last, in seconds. When the appearance and
	// the hold do not fit, the hold is cut and then the dissolve starts early.
	TotalMaxSec float64 `yaml:"total_max_sec"`
	// Loop plays the animation again when it ends, until a key is pressed.
	Loop bool `yaml:"loop"`

	// Color is the colour of the art: a hex colour such as #39ff14, or green, white, magenta, cyan.
	Color string `yaml:"color"`
	// Mode is how a character appears: "fade" grows it through a scale of denser glyphs,
	// "decode" shows random characters until it settles.
	Mode string `yaml:"mode"`
	// Seed makes the animation repeatable: the same non-zero seed gives the same frames. 0 picks a
	// new one each time.
	Seed uint64 `yaml:"seed"`
}

// DefaultConfig returns the starting values.
func DefaultConfig() Config {
	return Config{
		Speed: 1.8, FPS: 30,
		Rings: 6, Pulse: 5, Fade: 12, Jitter: 2,
		CenterX: 50, CenterY: 50,
		PulseFreq: 35, PulseSpeed: 40, PulseWidth: 18,
		Glitch: 55, RGBSplit: 50,
		Scanlines: true, Vignette: true, CellAspect: 0.5,
		FadeOutSec: 1.2, HoldSec: 2, TotalMaxSec: 6,
		Color: "#39ff14", Mode: "fade",
	}
}

// Modes of appearance.
const (
	ModeFade   = "fade"
	ModeDecode = "decode"
)

// Validate says what is wrong with the configuration, if anything.
func (c Config) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(c.Speed > 0, "speed must be above 0, got %v", c.Speed)
	check(c.FPS >= 1 && c.FPS <= 120, "fps must be from 1 to 120, got %d", c.FPS)
	check(c.Rings >= 1, "rings must be at least 1, got %d", c.Rings)
	check(c.Pulse >= 0, "pulse must not be negative, got %v", c.Pulse)
	check(c.Fade > 0, "fade must be above 0, got %v", c.Fade)
	check(c.Jitter >= 0, "jitter must not be negative, got %v", c.Jitter)
	check(c.CenterX >= 0 && c.CenterX <= 100, "center_x must be from 0 to 100, got %v", c.CenterX)
	check(c.CenterY >= 0 && c.CenterY <= 100, "center_y must be from 0 to 100, got %v", c.CenterY)
	check(c.PulseFreq >= 0 && c.PulseFreq <= 100, "pulse_freq must be from 0 to 100, got %v", c.PulseFreq)
	check(c.PulseSpeed >= 0, "pulse_speed must not be negative, got %v", c.PulseSpeed)
	check(c.PulseWidth >= 0, "pulse_width must not be negative, got %v", c.PulseWidth)
	check(c.Glitch >= 0 && c.Glitch <= 100, "glitch must be from 0 to 100, got %v", c.Glitch)
	check(c.RGBSplit >= 0 && c.RGBSplit <= 100, "rgb_split must be from 0 to 100, got %v", c.RGBSplit)
	check(c.CellAspect > 0, "cell_aspect must be above 0, got %v", c.CellAspect)
	check(c.FadeOutSec > 0, "fade_out_sec must be above 0, got %v", c.FadeOutSec)
	check(c.HoldSec >= 0, "hold_sec must not be negative, got %v", c.HoldSec)
	check(c.TotalMaxSec > 0, "total_max_sec must be above 0, got %v", c.TotalMaxSec)
	check(c.Mode == ModeFade || c.Mode == ModeDecode, "mode must be %q or %q, got %q", ModeFade, ModeDecode, c.Mode)
	if _, err := ParseColor(c.Color); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RGB is a colour.
type RGB struct{ R, G, B uint8 }

var presets = map[string]RGB{
	"green":   {0x39, 0xff, 0x14},
	"white":   {0xff, 0xff, 0xff},
	"magenta": {0xff, 0x00, 0xff},
	"cyan":    {0x00, 0xff, 0xff},
}

// ParseColor reads a preset name or a hex colour (#rrggbb or #rgb).
func ParseColor(s string) (RGB, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := presets[s]; ok {
		return c, nil
	}
	digits, ok := strings.CutPrefix(s, "#")
	if !ok {
		return RGB{}, fmt.Errorf("color %q is neither a preset (green, white, magenta, cyan) nor a hex colour", s)
	}
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	b, err := hex.DecodeString(digits)
	if err != nil || len(b) != 3 {
		return RGB{}, fmt.Errorf("color %q is not #rrggbb or #rgb", s)
	}
	return RGB{b[0], b[1], b[2]}, nil
}

// LoadYAML reads a configuration over the defaults: a field the file does not mention keeps its
// default, and a field the file spells wrongly is an error.
func LoadYAML(data []byte) (Config, error) {
	cfg := DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("splash config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
