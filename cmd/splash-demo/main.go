// Command splash-demo plays the opening animation, to try its settings. Every field of
// splash.Config is a flag, and -config reads them from a YAML file; a flag beats the file, and the
// file beats the defaults.
//
//	go run ./cmd/splash-demo -glitch 80 -color cyan -loop
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/sozureke/kagegami/internal/splash"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "splash-demo:", err)
		os.Exit(1)
	}
}

func run() error {
	def := splash.DefaultConfig()
	var fc splash.Config // what the flags were given; only the flags that were set are used
	// apply copies a field from the flags to the configuration.
	apply := map[string]func(dst *splash.Config){}
	flt := func(name string, field func(*splash.Config) *float64, usage string) {
		flag.Float64Var(field(&fc), name, *field(&def), usage)
		apply[name] = func(dst *splash.Config) { *field(dst) = *field(&fc) }
	}
	num := func(name string, field func(*splash.Config) *int, usage string) {
		flag.IntVar(field(&fc), name, *field(&def), usage)
		apply[name] = func(dst *splash.Config) { *field(dst) = *field(&fc) }
	}
	boolean := func(name string, field func(*splash.Config) *bool, usage string) {
		flag.BoolVar(field(&fc), name, *field(&def), usage)
		apply[name] = func(dst *splash.Config) { *field(dst) = *field(&fc) }
	}
	str := func(name string, field func(*splash.Config) *string, usage string) {
		flag.StringVar(field(&fc), name, *field(&def), usage)
		apply[name] = func(dst *splash.Config) { *field(dst) = *field(&fc) }
	}

	flt("speed", func(c *splash.Config) *float64 { return &c.Speed }, "time multiplier; higher is faster")
	num("fps", func(c *splash.Config) *int { return &c.FPS }, "frames per second; higher is smoother")
	num("rings", func(c *splash.Config) *int { return &c.Rings }, "rings the art appears in; more is a finer ripple")
	flt("pulse", func(c *splash.Config) *float64 { return &c.Pulse }, "pause between rings, in frames")
	flt("fade", func(c *splash.Config) *float64 { return &c.Fade }, "frames a character takes to appear")
	flt("jitter", func(c *splash.Config) *float64 { return &c.Jitter }, "random spread of the start of a character, in frames")
	flt("center-x", func(c *splash.Config) *float64 { return &c.CenterX }, "centre of the rings, % of the art's width")
	flt("center-y", func(c *splash.Config) *float64 { return &c.CenterY }, "centre of the rings, % of the art's height")
	flt("pulse-freq", func(c *splash.Config) *float64 { return &c.PulseFreq }, "how often a pulse starts, 0-100")
	flt("pulse-speed", func(c *splash.Config) *float64 { return &c.PulseSpeed }, "how fast a pulse travels")
	flt("pulse-width", func(c *splash.Config) *float64 { return &c.PulseWidth }, "thickness of a pulse, in tenths of a cell")
	flt("glitch", func(c *splash.Config) *float64 { return &c.Glitch }, "amount of glitching, 0-100; 0 turns it off")
	flt("rgb-split", func(c *splash.Config) *float64 { return &c.RGBSplit }, "red and cyan ghosts on glitching rows, 0-100")
	boolean("scanlines", func(c *splash.Config) *bool { return &c.Scanlines }, "darken every second row (-scanlines=false to turn off)")
	boolean("vignette", func(c *splash.Config) *bool { return &c.Vignette }, "darken towards the edges (-vignette=false to turn off)")
	flt("cell-aspect", func(c *splash.Config) *float64 { return &c.CellAspect }, "width / height of a terminal cell")
	flt("fade-out-sec", func(c *splash.Config) *float64 { return &c.FadeOutSec }, "seconds the art takes to dissolve")
	flt("hold-sec", func(c *splash.Config) *float64 { return &c.HoldSec }, "seconds the finished art glitches before it dissolves")
	flt("total-max-sec", func(c *splash.Config) *float64 { return &c.TotalMaxSec }, "longest the whole animation may last, in seconds")
	boolean("loop", func(c *splash.Config) *bool { return &c.Loop }, "play again when it ends, until a key is pressed")
	str("color", func(c *splash.Config) *string { return &c.Color }, "hex colour such as #39ff14, or green, white, magenta, cyan")
	str("mode", func(c *splash.Config) *string { return &c.Mode }, "fade or decode")
	seed := flag.Uint64("seed", 0, "repeatable animation: the same non-zero seed gives the same frames; 0 is random")
	file := flag.String("config", "", "a YAML file with the settings (see docs in the README)")
	artFile := flag.String("art", "", "play this file instead of the built-in art")
	flag.Parse()

	cfg := def
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if cfg, err = splash.LoadYAML(data); err != nil {
			return err
		}
	}
	flag.Visit(func(f *flag.Flag) {
		if set, ok := apply[f.Name]; ok {
			set(&cfg)
		}
		if f.Name == "seed" {
			cfg.Seed = *seed
		}
	})
	if err := cfg.Validate(); err != nil {
		return err
	}

	art := splash.Art
	if *artFile != "" {
		data, err := os.ReadFile(*artFile)
		if err != nil {
			return err
		}
		art = string(data)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return splash.Run(ctx, art, cfg)
}
