package tui

import (
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// marshalArt is one MARSHAL wordmark. unicode marks the ones that need box or
// block glyphs, so a terminal without Unicode is only ever shown the ASCII
// ones.
type marshalArt struct {
	unicode bool
	lines   []string
}

// marshalBanners are the wordmarks the splash rotates through, so a person
// starting the Marshal sees a different one each time rather than the same
// screen. Each is a fixed block; only the subtitle below it animates.
var marshalBanners = []marshalArt{
	{unicode: false, lines: []string{
		`  __  __   _   ___  ___ _  _   _   _     `,
		` |  \/  | /_\ | _ \/ __| || | /_\ | |    `,
		` | |\/| |/ _ \|   /\__ \ __ |/ _ \| |__  `,
		` |_|  |_/_/ \_\_|_\|___/_||_/_/ \_\____| `,
	}},
	{unicode: false, lines: []string{
		` M   M   AAA   RRRR   SSSS  H   H   AAA   L     `,
		` MM MM  A   A  R   R  S     H   H  A   A  L     `,
		` M M M  AAAAA  RRRR    SSS  HHHHH  AAAAA  L     `,
		` M   M  A   A  R  R       S H   H  A   A  L     `,
		` M   M  A   A  R   R  SSSS  H   H  A   A  LLLLL `,
	}},
	{unicode: false, lines: []string{
		` _|_|_|_|  MARSHAL  _|_|_|_| `,
		` >>  one model plans,  <<    `,
		` >>  agents do the work  <<  `,
	}},
	{unicode: true, lines: []string{
		`███╗   ███╗ █████╗ ██████╗ ███████╗██╗  ██╗ █████╗ ██╗     `,
		`████╗ ████║██╔══██╗██╔══██╗██╔════╝██║  ██║██╔══██╗██║     `,
		`██╔████╔██║███████║██████╔╝███████╗███████║███████║██║     `,
		`██║╚██╔╝██║██╔══██║██╔══██╗╚════██║██╔══██║██╔══██║██║     `,
		`██║ ╚═╝ ██║██║  ██║██║  ██║███████║██║  ██║██║  ██║███████╗`,
		`╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝`,
	}},
	{unicode: true, lines: []string{
		`╔╦╗╔═╗╦═╗╔═╗╦ ╦╔═╗╦  `,
		`║║║╠═╣╠╦╝╚═╗╠═╣╠═╣║  `,
		`╩ ╩╩ ╩╩╚═╚═╝╩ ╩╩ ╩╩═╝`,
	}},
}

var marshalSplashMu struct {
	sync.Mutex
	last int
}

// pickMarshalBanner returns a wordmark the terminal can render, avoiding the
// one shown just before so the rotation is visible rather than repeating.
func pickMarshalBanner(unicode bool) marshalArt {
	eligible := make([]int, 0, len(marshalBanners))
	for i, art := range marshalBanners {
		if art.unicode && !unicode {
			continue
		}
		eligible = append(eligible, i)
	}
	if len(eligible) == 0 {
		return marshalBanners[0]
	}
	marshalSplashMu.Lock()
	defer marshalSplashMu.Unlock()
	choice := eligible[rand.Intn(len(eligible))]
	if len(eligible) > 1 {
		for choice == marshalSplashMu.last {
			choice = eligible[rand.Intn(len(eligible))]
		}
	}
	marshalSplashMu.last = choice
	return marshalBanners[choice]
}

// playMarshalSplash shows a MARSHAL wordmark while the Marshal model is
// starting, so the person sees MARSHAL taking the helm rather than a blank
// pause or a wall of briefing text. It writes to out and returns quickly; the
// launch that follows is what the person waits on, not this. A different
// wordmark is shown each time.
//
// With animation off, or when out is not the live terminal, it prints the
// banner once with no timing, so a captured or piped session stays clean.
func playMarshalSplash(out io.Writer, th *Theme, animate bool) {
	if out == nil || th == nil {
		return
	}
	lines := pickMarshalBanner(th.Unicode).lines
	// The subtitle never names the underlying provider: the person is working
	// with MARSHAL, not with whatever model happens to sit behind it.
	subtitle := "Summoning the Marshal"

	if !animate {
		fmt.Fprintln(out)
		for _, line := range lines {
			fmt.Fprintln(out, th.Colorize(th.Marshal, line))
		}
		fmt.Fprintln(out, th.Colorize(th.Muted, "  "+subtitle))
		fmt.Fprintln(out)
		return
	}

	// Reveal the wordmark line by line, so it draws itself rather than
	// appearing all at once.
	fmt.Fprintln(out)
	for _, line := range lines {
		fmt.Fprintln(out, th.Colorize(th.Marshal, line))
		time.Sleep(70 * time.Millisecond)
	}

	// A short colour sweep across the subtitle, then a settled line. Three
	// passes keep it under a third of a second.
	for pass := 0; pass < 3; pass++ {
		fmt.Fprintf(out, "\r%s", th.Colorize(marshalSweepColor(th, pass), "  "+subtitle+strings.Repeat(".", pass+1)))
		time.Sleep(90 * time.Millisecond)
	}
	fmt.Fprintf(out, "\r%s\n\n", th.Colorize(th.Muted, "  "+subtitle+"..."))
}

// marshalSweepColor alternates the sweep between the brand and accent colours,
// falling back to the brand colour when the theme has no accent.
func marshalSweepColor(th *Theme, pass int) string {
	if pass%2 == 1 && th.Accent != "" {
		return th.Accent
	}
	if th.Marshal != "" {
		return th.Marshal
	}
	return th.Accent
}
