package main

import (
	"strings"

	courrier "courrier/mail"
)

// openMailto opens a compose window for a mailto: link.
func (a *app) openMailto(raw string) {
	m, err := courrier.ParseMailto(raw)
	if err != nil {
		a.toast = "Couldn't open the link: " + err.Error()
		return
	}

	if len(a.accounts) == 0 {
		a.toast = "Add an account to write mail."
		return
	}

	a.compose(m)
}

// secondInstance follows a second launch of courrier: its mailto: links
// reach OnOpenURL, and anything else shows the window.
func (a *app) secondInstance(args []string) {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "mailto:") {
			return
		}
	}

	if a.showWindow != nil {
		a.showWindow()
	}
}
