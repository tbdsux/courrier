package main

import (
	"embed"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
)

// JetBrains Mono NL (no code ligatures, so "->" in mail stays as typed),
// under the SIL Open Font License in fonts/OFL.txt.
//
//go:embed fonts/*.ttf
var fontFiles embed.FS

const fontFamily = "JetBrains Mono"

func registerFonts() {
	entries, _ := fontFiles.ReadDir("fonts")
	for _, e := range entries {
		data, _ := fontFiles.ReadFile("fonts/" + e.Name())
		if err := ui.RegisterFont(data, fontFamily); err != nil {
			log.Println("font:", e.Name(), err) // the system font stands in
		}
	}
}

// The sign-in providers' logos, from their branding guidelines, shown in
// their own colors.
var (
	//go:embed resources/google.svg
	googleSVG []byte
	//go:embed resources/microsoft.svg
	microsoftSVG []byte
	logos        = map[string]*ui.SVG{"google": ui.MustParseSVG(googleSVG), "microsoft": ui.MustParseSVG(microsoftSVG)}
)

// signInButton is "Sign in with Google" or "Microsoft", with the logo.
func signInButton(c *ui.Context, auth, name string) ui.Element {
	label := "Sign in with " + name
	return ui.Button(c, "").Label(label).Padding(8, 14).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			// Google's dark-theme button keeps the "G" on white.
			ui.Column(c).Size(22, 22).Radius(4).Background(ui.Hex("#ffffff")).Center().Children(func() {
				ui.Image(c, logos[auth]).Size(18, 18)
			})
			ui.Text(c, label).FontWeight(500)
		})
	})
}

// lucide makes an icon from Lucide's shapes (ISC license).
func lucide(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	plusIcon      = lucide(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	inboxIcon     = lucide(`<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	sentIcon      = lucide(`<path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/>`)
	draftsIcon    = lucide(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>`)
	archiveIcon   = lucide(`<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>`)
	junkIcon      = lucide(`<circle cx="12" cy="12" r="10"/><path d="m4.9 4.9 14.2 14.2"/>`)
	trashIcon     = lucide(`<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>`)
	folderIcon    = lucide(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	flagIcon      = lucide(`<path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"/><line x1="4" x2="4" y1="22" y2="15"/>`)
	paperclipIcon = lucide(`<path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"/>`)
	userIcon      = lucide(`<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`)
	driveIcon     = lucide(`<line x1="22" x2="2" y1="12" y2="12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/><line x1="6" x2="6.01" y1="16" y2="16"/><line x1="10" x2="10.01" y1="16" y2="16"/>`)
	serverIcon    = lucide(`<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>`)
	settingsIcon  = lucide(`<path d="M20 7h-9"/><path d="M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`)
	refreshIcon   = lucide(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)
	backIcon      = lucide(`<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>`)
	composeIcon   = lucide(`<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z"/>`)
	replyIcon     = lucide(`<polyline points="9 17 4 12 9 7"/><path d="M20 18v-2a4 4 0 0 0-4-4H4"/>`)
	replyAllIcon  = lucide(`<polyline points="7 17 2 12 7 7"/><polyline points="12 17 7 12 12 7"/><path d="M22 18v-2a4 4 0 0 0-4-4H7"/>`)
	forwardIcon   = lucide(`<polyline points="15 17 20 12 15 7"/><path d="M4 18v-2a4 4 0 0 1 4-4h12"/>`)
	boldIcon      = lucide(`<path d="M6 12h9a4 4 0 0 1 0 8H7a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h7a4 4 0 0 1 0 8"/>`)
	italicIcon    = lucide(`<line x1="19" x2="10" y1="4" y2="4"/><line x1="14" x2="5" y1="20" y2="20"/><line x1="15" x2="9" y1="4" y2="20"/>`)
	linkIcon      = lucide(`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`)
	listIcon      = lucide(`<path d="M3 12h.01"/><path d="M3 18h.01"/><path d="M3 6h.01"/><path d="M8 12h13"/><path d="M8 18h13"/><path d="M8 6h13"/>`)
	orderedIcon   = lucide(`<path d="M10 12h11"/><path d="M10 18h11"/><path d="M10 6h11"/><path d="M4 10h2"/><path d="M4 6h1v4"/><path d="M6 18H4c0-1 2-2 2-3s-1-1.5-2-1"/>`)
	quoteIcon     = lucide(`<path d="M17 6H3"/><path d="M21 12H8"/><path d="M21 18H8"/><path d="M3 12v6"/>`)
	codeIcon      = lucide(`<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>`)
	mailIcon      = lucide(`<rect width="20" height="16" x="2" y="4" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/>`)
	moveIcon      = lucide(`<path d="M2 9V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H20a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-1.73"/><path d="M2 13h10"/><path d="m9 16 3-3-3-3"/>`)
	filterIcon    = lucide(`<polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/>`)
	clockIcon     = lucide(`<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>`)
	searchIcon    = lucide(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)
	bookmarkIcon  = lucide(`<path d="m19 21-7-4-7 4V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v16z"/>`)
	outboxIcon    = lucide(`<path d="M12 13V2"/><path d="m8 6 4-4 4 4"/><path d="M2 13h4l2 3h8l2-3h4v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2z"/>`)
)

// specialIcons are the icons of special folders.
var specialIcons = map[string]*ui.SVG{"inbox": inboxIcon, "sent": sentIcon, "drafts": draftsIcon,
	"archive": archiveIcon, "junk": junkIcon, "trash": trashIcon, "snoozed": clockIcon}

// palette holds the colors the theme has no slot for.
type palette struct {
	sidebar, card, accentSoft ui.Color
}

// look is one of the themes in Settings, its colors as hex.
type look struct {
	dark                                             bool
	bg, surface, hover, pressed, border, text, muted string
	accent, accentHover, accentPressed, accentText   string
	success, danger, sidebar, card                   string
}

// appearances are the themes in Settings, in order; "System" follows the
// system's light or dark appearance.
var appearances = []string{"System", "Light", "Dark", "Solarized Light", "Nord", "Dracula", "Gruvbox"}

var looks = map[string]look{
	"Light":           {false, "#fbfbfc", "#f0f0f3", "#e7e7ec", "#dedee4", "#e3e3e8", "#17171c", "#73737f", "#6e56cf", "#5f48c2", "#5039b3", "#ffffff", "#12a26b", "#d93d42", "#f3f3f6", "#ffffff"},
	"Dark":            {true, "#111113", "#1f1f23", "#28282d", "#313137", "#2c2c32", "#ececf1", "#8b8b96", "#8e7cf6", "#a495f8", "#7b67f0", "#ffffff", "#3ddc97", "#ff6b6b", "#0b0b0d", "#17171a"},
	"Solarized Light": {false, "#fdf6e3", "#eee8d5", "#e6dfc8", "#ddd6bf", "#e0d9c3", "#073642", "#657b83", "#268bd2", "#2078b8", "#1b6aa3", "#ffffff", "#859900", "#dc322f", "#f5eedb", "#fffbef"},
	"Nord":            {true, "#2e3440", "#3b4252", "#434c5e", "#4c566a", "#434c5e", "#eceff4", "#9aa5b8", "#88c0d0", "#8fbcbb", "#81a1c1", "#2e3440", "#a3be8c", "#bf616a", "#272c36", "#343a46"},
	"Dracula":         {true, "#282a36", "#343746", "#3e4153", "#44475a", "#44475a", "#f8f8f2", "#9099c0", "#bd93f9", "#caa7fa", "#a87ef5", "#282a36", "#50fa7b", "#ff5555", "#21222c", "#2d2f3d"},
	"Gruvbox":         {true, "#282828", "#3c3836", "#504945", "#665c54", "#3c3836", "#ebdbb2", "#a89984", "#fe8019", "#fe9a4a", "#d65d0e", "#282828", "#b8bb26", "#fb4934", "#1d2021", "#32302f"},
}

// styled returns the app's look in the theme named appearance on top of
// the default theme; "System" (or a name it doesn't know) follows system.
func styled(system *ui.Theme, appearance string, fontSize float32) (*ui.Theme, palette) {
	l, ok := looks[appearance]
	base := system
	switch {
	case !ok && system.Dark:
		l = looks["Dark"]
	case !ok:
		l = looks["Light"]
	case l.dark:
		base = ui.DarkTheme()
	default:
		base = ui.LightTheme()
	}
	t := *base
	t.Font, t.FontSize, t.Radius, t.Spacing = fontFamily, fontSize, 8, 4
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = ui.Hex(l.bg), ui.Hex(l.surface), ui.Hex(l.hover), ui.Hex(l.pressed)
	t.Border, t.Text, t.TextMuted = ui.Hex(l.border), ui.Hex(l.text), ui.Hex(l.muted)
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex(l.accent), ui.Hex(l.accentHover), ui.Hex(l.accentPressed), ui.Hex(l.accentText)
	t.Success, t.Danger = ui.Hex(l.success), ui.Hex(l.danger)
	t.Selection, t.Focus = t.Accent.Alpha(0.28), t.Accent.Alpha(0.55)
	return &t, palette{sidebar: ui.Hex(l.sidebar), card: ui.Hex(l.card), accentSoft: t.Accent.Alpha(0.14)}
}

// eyebrow is the small upper-case label above a page's title.
func eyebrow(c *ui.Context, s string) {
	ui.Text(c, strings.ToUpper(s)).FontSize(11).FontWeight(500).LetterSpacing(1.2).TextColor(c.Theme().Accent)
}

// header titles a page.
func header(c *ui.Context, kicker, title, sub string) {
	ui.Column(c).Gap(6).Padding(0, 0, 8, 0).Children(func() {
		eyebrow(c, kicker)
		ui.Text(c, title).FontSize(24).Bold().LetterSpacing(-0.5)
		if sub != "" {
			ui.Text(c, sub).TextColor(c.Theme().TextMuted).Wrap()
		}
	})
}

// card groups a section of a page under a title.
func (a *app) card(c *ui.Context, key, title string, fn func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).Gap(14).Padding(18, 20).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontWeight(500).TextColor(t.TextMuted)
		fn()
	})
}

// notice is a tinted line of status.
// iconButton is a small square button showing an icon, named label.
func iconButton(c *ui.Context, icon *ui.SVG, label, tip string) ui.Element {
	t := c.Theme()
	b := ui.Button(c, "").Label(label).Tooltip(tip).
		Size(28, 28).Padding(0).Radius(7).Justify(ui.Center).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(16).TextColor(t.TextMuted) })
}

func notice(c *ui.Context, text string, color ui.Color) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10, 14).Radius(8).Background(color.Alpha(0.12)).Children(func() {
		ui.Column(c).Size(6, 6).Radius(3).Background(color)
		ui.Text(c, text).TextColor(color).Wrap().Grow(1)
	})
}

// Layout breakpoints, in DIPs.
const (
	narrowContent = 600 // room beside the sidebar below which pages tighten
	pageMaxWidth  = 720
)

func (a *app) contentWidth() float32 { return a.width - a.sidebarW }

// page lays out a scrolling page: centered and capped on wide windows,
// with less padding on narrow ones.
func (a *app) page(c *ui.Context, gap float32, fn func()) {
	pad := float32(32)
	if a.contentWidth() < narrowContent {
		pad = 18
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(gap).Padding(pad, pad, 40, pad).FillWidth().MaxWidth(pageMaxWidth).Children(fn)
		})
	})
}

// formIn puts labels beside their fields when width leaves room for
// that, and above them otherwise.
func (a *app) formIn(c *ui.Context, width float32, fn func()) {
	if width < narrowContent-80 {
		ui.Column(c).Gap(16).Children(fn)
		return
	}
	ui.Form(c, fn).Gap(16)
}

// titleBar draws the title bar of a window with a hidden one, clear of the
// system's window controls. It draws nothing where they hide, as in full screen.
func titleBar(c *ui.Context, title string, bg ui.Color) {
	bar := c.TitleBar()
	if bar.Height == 0 {
		return
	}
	ui.Row(c).Height(max(bar.Height, 32)).Padding(0, bar.Right+12, 0, bar.Left+12).AlignItems(ui.Center).Justify(ui.Center).
		Background(bg).DragWindow().Children(func() {
		ui.Text(c, title).FontSize(12).Bold().TextColor(c.Theme().TextMuted).SingleLine()
	})
}
