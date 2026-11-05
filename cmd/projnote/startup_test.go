package main

import "testing"

func TestDesktopStartupDefaultAndExplicitServiceModes(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		compact, browser, noBrowser, noDesktop bool
		preference                             string
		want                                   desktopStartupOptions
	}{
		{name: "normal startup is launcher only", preference: "1", want: desktopStartupOptions{desktop: true}},
		{name: "legacy preference keeps launcher only", want: desktopStartupOptions{desktop: true}},
		{name: "saved launcher opt out remains service only", preference: "0", want: desktopStartupOptions{}},
		{name: "no browser compatibility remains headless", noBrowser: true, preference: "1", want: desktopStartupOptions{}},
		{name: "no desktop does not implicitly launch browser", noDesktop: true, preference: "1", want: desktopStartupOptions{}},
		{name: "browser must be explicit", browser: true, preference: "1", want: desktopStartupOptions{desktop: true, browser: true}},
		{name: "browser only", browser: true, noDesktop: true, preference: "1", want: desktopStartupOptions{browser: true}},
		{name: "browser honours launcher preference", browser: true, preference: "0", want: desktopStartupOptions{browser: true}},
		{name: "explicit compact overrides preference", compact: true, preference: "0", want: desktopStartupOptions{desktop: true, expand: true}},
		{name: "compact with no browser remains native", compact: true, noBrowser: true, preference: "1", want: desktopStartupOptions{desktop: true, expand: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := desktopStartup(tc.compact, tc.browser, tc.noBrowser, tc.noDesktop, tc.preference)
			if got != tc.want {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}
func TestConflictingStartupFlagsAreExplicit(t *testing.T) {
	for _, flags := range [][4]bool{{true, false, false, true}, {false, true, true, false}, {true, true, false, false}} {
		if validateStartupFlags(flags[0], flags[1], flags[2], flags[3]) == nil {
			t.Fatalf("contradictory flags accepted: %v", flags)
		}
	}
	for _, flags := range [][4]bool{{}, {false, true, false, true}, {true, false, true, false}} {
		if err := validateStartupFlags(flags[0], flags[1], flags[2], flags[3]); err != nil {
			t.Fatal(err)
		}
	}
}
