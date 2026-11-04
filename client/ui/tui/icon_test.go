package tui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"
)

func TestIconFitting(t *testing.T) {
	braille := strings.Repeat("⣿", 30)
	var art []string
	for i := 0; i < 15; i++ {
		art = append(art, braille)
	}
	out := fitIcon(art, iconW, iconH)
	if len(out) > iconH || len([]rune(out[0])) > iconW || out[0][0] == ' ' {
		t.Fatalf("braille not scaled into the box: %d×%d", len(out), len([]rune(out[0])))
	}
	// A hollow square keeps its inside empty when scaled by dot share.
	hollow := []string{"⣿⣿⣿⣿⣿⣿", "⣿⠀⠀⠀⠀⣿", "⣿⠀⠀⠀⠀⣿", "⣿⣿⣿⣿⣿⣿"}
	small := fitIcon(hollow, 3, 2)
	if strings.ContainsRune(small[0][1:2]+small[1][1:2], '⣿') {
		t.Fatalf("inside filled in: %q", small)
	}
	dots := []string{" •●●:.        .:●●•", ":●●●●:        :●●●●:"}
	for len(dots) < 14 {
		dots = append(dots, "   ●●●●●●●●●●●●")
	}
	if out = fitIcon(dots, iconW, iconH); len(out) > iconH {
		t.Fatalf("art not sampled: %d rows", len(out))
	}
	for _, icon := range [][]string{standardIcon(nil), compactIcon(nil)} {
		w := lipgloss.Width(icon[0])
		for _, l := range icon {
			if lipgloss.Width(l) != w {
				t.Fatalf("icon box not uniform: %q", icon)
			}
		}
	}
	if len(standardIcon(nil)) != iconH || len(compactIcon(nil)) != compactIconH || lipgloss.Width(standardIcon(nil)[0]) != iconW {
		t.Fatal("robot icon not in the standard boxes")
	}
}
