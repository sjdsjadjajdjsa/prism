package main

import (
	"fmt"
	"math"
	"strings"
)

type rgb struct{ r, g, b uint8 }

type theme struct {
	name                                    string
	bg, panel, fg, muted, faint, red, green rgb
	spectrum                                []rgb
}

func hex(s string) rgb {
	var c rgb
	fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &c.r, &c.g, &c.b)
	return c
}

func hexes(list ...string) []rgb {
	out := make([]rgb, len(list))
	for i, s := range list {
		out[i] = hex(s)
	}
	return out
}

func mix(a, b rgb, t float64) rgb {
	t = math.Max(0, math.Min(1, t))
	lerp := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return rgb{lerp(a.r, b.r), lerp(a.g, b.g), lerp(a.b, b.b)}
}

func (t theme) at(v float64) rgb {
	v = v - math.Floor(v)
	n := len(t.spectrum)
	p := v * float64(n)
	i := int(p) % n
	return mix(t.spectrum[i], t.spectrum[(i+1)%n], p-math.Floor(p))
}

var themes = []theme{
	{
		name: "prism", bg: hex("#0b0b10"), panel: hex("#14141c"), fg: hex("#e8e6f0"), muted: hex("#6e6a86"),
		faint: hex("#1d1c29"), red: hex("#ff6b81"), green: hex("#7ee2a8"),
		spectrum: hexes("#ff5f6d", "#ffa25f", "#f9e27d", "#7ee2a8", "#6fd3ff", "#8a9bff", "#c69bff"),
	},
	{
		name: "tokyo-night", bg: hex("#1a1b26"), panel: hex("#222436"), fg: hex("#c0caf5"), muted: hex("#565f89"),
		faint: hex("#292e42"), red: hex("#f7768e"), green: hex("#9ece6a"),
		spectrum: hexes("#f7768e", "#ff9e64", "#e0af68", "#9ece6a", "#7dcfff", "#7aa2f7", "#bb9af7"),
	},
	{
		name: "catppuccin", bg: hex("#1e1e2e"), panel: hex("#262637"), fg: hex("#cdd6f4"), muted: hex("#6c7086"),
		faint: hex("#313244"), red: hex("#f38ba8"), green: hex("#a6e3a1"),
		spectrum: hexes("#f38ba8", "#fab387", "#f9e2af", "#a6e3a1", "#89dceb", "#89b4fa", "#cba6f7"),
	},
	{
		name: "rose-pine", bg: hex("#191724"), panel: hex("#1f1d2e"), fg: hex("#e0def4"), muted: hex("#6e6a86"),
		faint: hex("#26233a"), red: hex("#eb6f92"), green: hex("#9ccfd8"),
		spectrum: hexes("#eb6f92", "#f6c177", "#ebbcba", "#9ccfd8", "#3e8fb0", "#c4a7e7"),
	},
	{
		name: "matte-black", bg: hex("#121212"), panel: hex("#1a1a1a"), fg: hex("#d6d6d6"), muted: hex("#6b6b6b"),
		faint: hex("#222222"), red: hex("#d96c6c"), green: hex("#93c47d"),
		spectrum: hexes("#f5a524", "#f2f2f2", "#9a9a9a", "#f5a524"),
	},
	{
		name: "gruvbox", bg: hex("#282828"), panel: hex("#32302f"), fg: hex("#ebdbb2"), muted: hex("#928374"),
		faint: hex("#3c3836"), red: hex("#fb4934"), green: hex("#b8bb26"),
		spectrum: hexes("#fb4934", "#fe8019", "#fabd2f", "#b8bb26", "#8ec07c", "#83a598", "#d3869b"),
	},
	{
		name: "nord", bg: hex("#2e3440"), panel: hex("#3b4252"), fg: hex("#d8dee9"), muted: hex("#7b88a1"),
		faint: hex("#434c5e"), red: hex("#bf616a"), green: hex("#a3be8c"),
		spectrum: hexes("#bf616a", "#d08770", "#ebcb8b", "#a3be8c", "#88c0d0", "#81a1c1", "#b48ead"),
	},
	{
		name: "everforest", bg: hex("#2d353b"), panel: hex("#343f44"), fg: hex("#d3c6aa"), muted: hex("#859289"),
		faint: hex("#3d484d"), red: hex("#e67e80"), green: hex("#a7c080"),
		spectrum: hexes("#e67e80", "#e69875", "#dbbc7f", "#a7c080", "#83c092", "#7fbbb3", "#d699b6"),
	},
}
