package main

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	courrier "courrier/mail"
	"courrier/store"

	"github.com/egoist/mygo/ui"
)

// The waits of compose: the undo window of Send, and how long after the
// last edit a draft is saved here and on the server.
var (
	undoWindow = 5 * time.Second
	localSave  = 2 * time.Second
	serverSave = 30 * time.Second
)

// sizeWarning is the total of attachments most servers refuse.
const sizeWarning = 25 << 20

// compose is a message being written, in a window of its own.
type compose struct {
	a           *app
	d           store.Draft
	from        string // the From select's choice
	to, cc, bcc []string
	showBcc     bool
	fresh       bool // made here, not reopened: closing it unedited discards it
	edited      bool // since the window opened
	snap        string
	gen         int // edits, which the save timers check
	savedAt     time.Time
	saveErr     string
	errs        map[string]string
	askSubject  bool
	askClose    bool
	done        bool // sent, kept or discarded: the window goes
	closing     bool // in the window's close event, which closes it
	titled      string
	serverBusy  bool
	serverAgain bool
	suggest     []string
	toast       string
	preview     bool      // the formatted message beside the markdown
	sel         selection // the message's, as last seen
	selPending  bool      // sel was set by the toolbar: select it in the text area
	sig         string    // the signature block as inserted, which a new From swaps; "" for none
	sigPos      int       // where an empty sig is, while the text is sigText; -1 when unknown
	sigText     string
	at          time.Time // Send later's time; zero to send now
	later       laterAsk

	// Set by whoever opens the window.
	update    func(func()) // runs a function on the main thread and redraws the window
	close     func()       // closes the window without asking
	setTitle  func(string)
	pickFiles func() ([]string, error)
}

func splitList(s string) []string {
	var out []string
	for _, a := range courrier.Addrs(s) {
		out = append(out, courrier.FormatAddr(a))
	}

	return out
}

// fromOf is the From of an account's own address.
func (a *app) fromOf(id int64) string {
	for _, acc := range a.accounts {
		if acc.ID == id {
			return courrier.FormatAddr(&netmail.Address{Name: acc.DisplayName, Address: acc.Email})
		}
	}
	return ""
}

// identsOf returns the addresses an account sends as: its own first,
// then its others.
func (a *app) identsOf(acc store.Account) []store.Identity {
	out := []store.Identity{{AccountID: acc.ID, Name: acc.DisplayName, Email: acc.Email, Signature: acc.Signature}}
	for _, id := range a.idents {
		if id.AccountID == acc.ID {
			out = append(out, id)
		}
	}

	return out
}

func identFrom(id store.Identity) string {
	return courrier.FormatAddr(&netmail.Address{Name: id.Name, Address: id.Email})
}

// identFor finds the identity a From choice is.
func (a *app) identFor(from string) (store.Identity, bool) {
	for _, acc := range a.accounts {
		for _, id := range a.identsOf(acc) {
			if identFrom(id) == from {
				return id, true
			}
		}
	}
	return store.Identity{}, false
}

// replyIdent picks the identity a message was sent to, of those of its
// account: the first of its To, Cc, then Delivered-To addresses that is
// one, else the account's own.
func replyIdent(ids []store.Identity, to, cc string, delivered []string) store.Identity {
	addrs := append(courrier.Addrs(to), courrier.Addrs(cc)...)
	for _, d := range delivered {
		addrs = append(addrs, &netmail.Address{Address: strings.Trim(strings.TrimSpace(d), "<>")})
	}

	for _, ad := range addrs {
		for _, id := range ids {
			if strings.EqualFold(id.Email, ad.Address) {
				return id
			}
		}
	}

	return ids[0]
}

// deliveredTo returns a message's Delivered-To addresses, which name the
// address it came to when it came by Bcc or a list.
func deliveredTo(raw []byte) []string {
	m, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}

	return m.Header["Delivered-To"]
}

// openCompose opens d in a window: saved first when new, so a crash
// keeps it. It returns d's compose, nil when it isn't in a window.
func (a *app) openCompose(d store.Draft, fresh bool) *compose {
	if cm := a.composes[d.ID]; d.ID != 0 && cm != nil {
		return cm // open already
	}

	if d.AccountID == 0 && len(a.accounts) > 0 {
		d.AccountID = a.accounts[0].ID
	}

	if id, ok := a.identFor(d.From); !ok || id.AccountID != d.AccountID {
		d.From = a.fromOf(d.AccountID)
	}

	d.Open = true

	if err := a.st.SaveDraft(&d); err != nil {
		a.toast = "Couldn't save the draft: " + err.Error()
		return nil
	}

	cm := &compose{a: a, d: d, fresh: fresh, from: d.From, to: splitList(d.To), cc: splitList(d.Cc), bcc: splitList(d.Bcc),
		showBcc: d.Bcc != "", preview: a.st.Pref("ui.preview") == "1", update: a.update, close: func() {}, sigPos: -1}
	if sig := a.sigBlock(d.From); fresh && sigAt(d.Text, sig) >= 0 {
		cm.sig = sig
	}

	cm.suggest, _ = a.st.Contacts(500)
	cm.snap = cm.snapshot()
	a.composes[d.ID] = cm

	if a.openWindow != nil {
		a.openWindow(cm)
	}

	return cm
}

// noSigAt records where the signature would be in a new message whose
// identity has none, for a From picked before any edit to put one there.
func (cm *compose) noSigAt(pos int) {
	if cm != nil && cm.sig == "" {
		cm.sigPos, cm.sigText = pos, cm.d.Text
	}
}

// reopenDrafts opens the drafts that were open when courrier last quit
// or crashed.
func (a *app) reopenDrafts() {
	ds, err := a.st.OpenDrafts()
	if err != nil {
		a.toast = "Couldn't read the drafts: " + err.Error()
	}

	for _, d := range ds {
		a.openCompose(d, false)
	}
}

func (a *app) newMessage() { a.compose(courrier.Draft{}) }

// sigAt finds a signature block in text as inserted: at its end, or
// above a quote; -1 when it isn't, or sig is "".
func sigAt(text, sig string) int {
	if sig == "" {
		return -1
	}

	if strings.HasSuffix(text, sig) {
		return len(text) - len(sig)
	}

	return strings.Index(text, sig+"\n\n")
}

// sigBlock is what the signature of the identity from adds to a message,
// after the "-- " line that tells other clients where it starts; "" for none.
func (a *app) sigBlock(from string) string {
	if id, ok := a.identFor(from); ok && strings.TrimSpace(id.Signature) != "" {
		return "\n\n-- \n" + strings.TrimSpace(id.Signature)
	}
	return ""
}

// compose opens a new message, from the account of the mailbox shown,
// else the first.
func (a *app) compose(m courrier.Draft) {
	d := store.Draft{Draft: m}
	id, _ := a.mailbox()
	if f, ok := a.folder(id); ok {
		d.AccountID = f.AccountID
	} else if len(a.accounts) > 0 {
		d.AccountID = a.accounts[0].ID
	}

	d.From = a.fromOf(d.AccountID)
	d.Text += a.sigBlock(d.From)
	a.openCompose(d, true).noSigAt(len(d.Text))
}

func (cm *compose) title() string {
	if s := strings.TrimSpace(cm.d.Subject); s != "" {
		return s
	}
	return "New message"
}

func (cm *compose) snapshot() string {
	return fmt.Sprintf("%q %q %q %q %q %q %d", cm.from, cm.to, cm.cc, cm.bcc, cm.d.Subject, cm.d.Text, len(cm.d.Attached))
}

// syncFields copies the window's choices into the draft.
func (cm *compose) syncFields() {
	if id, ok := cm.a.identFor(cm.from); ok && cm.from != cm.d.From {
		if id.AccountID != cm.d.AccountID {
			if cm.d.ServerUID != 0 { // its copy is in the other account's Drafts
				if s := cm.a.syncer(cm.d.AccountID); s != nil {
					s.DeleteDraft(cm.d.ServerUID, func(error) {})
				}

				cm.d.ServerUID = 0
			}

			cm.d.AccountID = id.AccountID
		}

		i := sigAt(cm.d.Text, cm.sig)
		if cm.sig == "" && cm.d.Text == cm.sigText {
			i = cm.sigPos
		}

		if i >= 0 { // not edited: the new identity's takes its place
			sig := cm.a.sigBlock(cm.from)
			cm.d.Text, cm.sig = cm.d.Text[:i]+sig+cm.d.Text[i+len(cm.sig):], sig
			cm.sigPos, cm.sigText = i, cm.d.Text
		}
	}

	cm.d.To, cm.d.Cc, cm.d.Bcc = strings.Join(cm.to, ", "), strings.Join(cm.cc, ", "), strings.Join(cm.bcc, ", ")
	cm.d.From = cm.from
}

// track notices edits and schedules the saves after the last one.
func (cm *compose) track() {
	s := cm.snapshot()
	if s == cm.snap {
		return
	}

	cm.snap, cm.edited = s, true
	cm.gen++
	gen := cm.gen
	time.AfterFunc(localSave, func() {
		cm.update(func() {
			if cm.gen == gen && !cm.done {
				cm.saveLocal()
			}
		})
	})

	time.AfterFunc(serverSave, func() {
		cm.update(func() {
			if cm.gen == gen && !cm.done {
				cm.saveServer(nil)
			}
		})
	})
}

func (cm *compose) saveLocal() {
	cm.syncFields()

	if err := cm.a.st.SaveDraft(&cm.d); err != nil {
		cm.saveErr = "Couldn't save the draft: " + err.Error()
		return
	}

	cm.saveErr, cm.savedAt = "", time.Now()
}

// saveServer saves the draft in the IMAP Drafts folder in place of its
// last copy, then calls done (when not nil) with the outcome. POP3 drafts
// stay here.
func (cm *compose) saveServer(done func(error)) {
	a := cm.a
	s := a.syncer(cm.d.AccountID)
	if s == nil || a.syncers[cm.d.AccountID].pop3 {
		if done != nil {
			done(errNotConnected)
		}
		return
	}

	if cm.serverBusy { // one at a time, or both would replace the same copy
		cm.serverAgain = true

		if done != nil {
			done(errNotConnected)
		}

		return
	}

	cm.syncFields()
	m := cm.d.Mail()
	m.From = cm.from
	raw, _, err := courrier.Build(m, time.Now())
	if err != nil {
		if done != nil {
			done(err)
		}
		return
	}

	cm.serverBusy = true
	s.ReplaceDraft(raw, cm.d.ServerUID, func(uid uint32, err error) {
		cm.update(func() {
			cm.serverBusy = false

			switch {
			case err != nil:
			case cm.done && !cm.d.Open && done == nil: // sent or discarded meanwhile
				s.DeleteDraft(uid, func(error) {})

			default:
				cm.d.ServerUID = uid
				a.st.SetDraftUID(cm.d.ID, uid)
			}

			if done != nil {
				done(err)
			} else if cm.serverAgain && !cm.done {
				cm.serverAgain = false
				cm.saveServer(nil)
			}
		})
	})
}

var errNotConnected = fmt.Errorf("not connected")

// requestClose answers the window's close button: true keeps it open,
// to ask what to do with the draft.
func (cm *compose) requestClose() bool {
	cm.closing = true
	defer func() { cm.closing = false }()

	switch {
	case cm.done:
		return false

	case cm.a.quitting: // kept here as it is, to reopen next launch
		cm.saveLocal()
		cm.done = true
		return false

	case cm.fresh && !cm.edited || cm.empty():
		cm.discard()
		return false

	case !cm.edited:
		cm.keep()
		return false
	}

	cm.askClose = true

	return true
}

func (cm *compose) empty() bool {
	return len(cm.to)+len(cm.cc)+len(cm.bcc)+len(cm.d.Attached) == 0 && strings.TrimSpace(cm.d.Subject+cm.d.Text) == ""
}

func (cm *compose) finish() {
	cm.done = true
	delete(cm.a.composes, cm.d.ID)

	if !cm.closing {
		cm.close()
	}

	cm.a.update(func() {}) // the main window shows the outbox and toasts
}

// keep closes the window and keeps the draft: in the IMAP Drafts folder,
// in the local one for POP3, or here, reopening next launch, when the
// server can't be reached.
func (cm *compose) keep() {
	a := cm.a
	cm.saveLocal()

	if cm.saveErr != "" {
		cm.toast = cm.saveErr
		return
	}

	id := cm.d.ID
	if !cm.edited && cm.d.ServerUID != 0 { // the server's copy is this draft
		a.st.DeleteDraft(id)
		cm.finish()
		return
	}

	if sy := a.syncers[cm.d.AccountID]; sy != nil && sy.pop3 {
		m := cm.d.Mail()
		m.From = cm.from

		if raw, _, err := courrier.Build(m, time.Now()); err == nil && a.st.AddLocal(cm.d.AccountID, "drafts", raw) == nil {
			a.st.DeleteDraft(id)
			a.reloadMail()
		}

		cm.finish()
		return
	}

	cm.saveServer(func(err error) {
		if err == nil { // the server holds it now
			a.st.DeleteDraft(id)
		}
	})

	cm.finish()
}

// discard closes the window and deletes the draft, with its server copy.
func (cm *compose) discard() {
	a := cm.a
	a.st.DeleteDraft(cm.d.ID)

	if s := a.syncer(cm.d.AccountID); s != nil && cm.d.ServerUID != 0 {
		s.DeleteDraft(cm.d.ServerUID, func(error) {})
	}

	cm.d.Open = false
	cm.finish()
}

// invalid returns the tokens that aren't addresses.
func invalid(tokens []string) []string {
	var out []string
	for _, t := range tokens {
		if _, err := netmail.ParseAddress(t); err != nil {
			out = append(out, t)
		}
	}

	return out
}

func (cm *compose) validate() map[string]string {
	errs := map[string]string{}
	if len(cm.to)+len(cm.cc)+len(cm.bcc) == 0 {
		errs["to"] = "Add at least one recipient."
	}

	for key, list := range map[string][]string{"to": cm.to, "cc": cm.cc, "bcc": cm.bcc} {
		if bad := invalid(list); len(bad) > 0 {
			errs[key] = "Not an address: " + strings.Join(bad, ", ")
		}
	}

	return errs
}

// sendAt sends the message at at, or with zero now.
func (cm *compose) sendAt(at time.Time) {
	cm.at = at
	cm.send(false)
}

// send checks the message and puts it in the outbox; force sends it
// without a subject.
func (cm *compose) send(force bool) {
	cm.syncFields()

	if cm.errs = cm.validate(); len(cm.errs) > 0 {
		return
	}

	if !force && strings.TrimSpace(cm.d.Subject) == "" {
		cm.askSubject = true
		return
	}

	if err := cm.a.send(cm); err != nil {
		cm.errs["send"] = "Couldn't send: " + err.Error()
	}
}

// send puts a message in the outbox in place of its draft, and closes its
// window; it goes once the undo window has passed, or at cm.at.
func (a *app) send(cm *compose) error {
	m := cm.d.Mail()
	m.From = cm.from
	raw, rcpts, err := courrier.Build(m, time.Now())
	if err != nil {
		return err
	}

	after := time.Now().Add(undoWindow)
	if !cm.at.IsZero() {
		after = cm.at
	}

	o := courrier.Outgoing{Raw: raw, Rcpts: rcpts, SendAfter: after, ReplyPath: cm.d.ReplyPath, ReplyUID: cm.d.ReplyUID, DraftUID: cm.d.ServerUID}
	id, err := a.st.Enqueue(cm.d.AccountID, cm.d.ID, o, strings.Join([]string{m.To, m.Cc, m.Bcc}, ", "))
	if err != nil {
		return err
	}

	cm.d.Open = false
	cm.finish()

	if s := a.syncer(cm.d.AccountID); s != nil {
		s.SendNow()
	}

	if cm.at.IsZero() {
		a.undoID = id
	} else {
		a.toast = "Scheduled for " + laterLabel(cm.at, time.Now())
	}

	a.reloadMail()

	return nil
}

// saveTemplate keeps a copy of the message to start others from.
func (cm *compose) saveTemplate() {
	cm.syncFields()
	d := store.Draft{AccountID: cm.d.AccountID, Draft: cm.d.Draft, Template: true}
	d.InReplyTo, d.References = "", ""

	for _, f := range cm.d.Attached {
		d.Attached = append(d.Attached, store.DraftFile{File: f.File})
	}

	if err := cm.a.st.SaveDraft(&d); err != nil {
		cm.toast = "Couldn't save the template: " + err.Error()
		return
	}

	cm.a.templates = append(cm.a.templates, d)
	cm.toast = "Saved as a template"
}

// templatesMenu starts a new message from a template, or deletes one.
func (a *app) templatesMenu(m *ui.Menu) {
	for _, t := range a.templates {
		if m.Item(subject(t.Subject)).Chosen() {
			a.fromTemplate(t)
		}
	}

	m.Separator()

	m.Submenu("Delete template", func(sub *ui.Menu) {
		for _, t := range a.templates {
			if sub.Item(subject(t.Subject)).Chosen() {
				if err := a.st.DeleteDraft(t.ID); err != nil {
					a.toast = "Couldn't delete it: " + err.Error()
				}

				a.reload()
			}
		}
	})
}

// fromTemplate opens a new message, a copy of template t.
func (a *app) fromTemplate(t store.Draft) {
	d := t
	d.ID, d.Template, d.Attached = 0, false, nil
	for _, f := range t.Attached {
		d.Attached = append(d.Attached, store.DraftFile{File: f.File})
	}

	a.openCompose(d, true)
}

// takeBack moves a message of the outbox back into a compose window.
func (a *app) takeBack(id int64) {
	o, ok, err := a.st.TakeOutgoing(id)
	switch {
	case err != nil:
		a.toast = "Couldn't take it back: " + err.Error()
		return

	case !ok:
		a.toast = "It's sent already."
		return
	}

	a.reloadMail()
	md, err := courrier.DraftFrom(o.Raw)
	if err != nil {
		a.toast = "Couldn't read it back: " + err.Error()
		return
	}

	shown := map[string]bool{}
	for _, ad := range courrier.Addrs(md.To + ", " + md.Cc) {
		shown[strings.ToLower(ad.Address)] = true
	}

	var bcc []string
	for _, r := range o.Rcpts { // the envelope's other recipients were Bcc
		if !shown[strings.ToLower(r)] {
			bcc = append(bcc, r)
		}
	}

	md.Bcc = strings.Join(bcc, ", ")
	d := store.Draft{AccountID: o.AccountID, Draft: md, ReplyPath: o.ReplyPath, ReplyUID: o.ReplyUID, ServerUID: o.DraftUID}
	for _, f := range md.Files {
		d.Attached = append(d.Attached, store.DraftFile{File: f})
	}

	d.Files = nil
	a.openCompose(d, false)
}

// fileType names a file's MIME type from its extension, else its content.
func fileType(name string, data []byte) string {
	t := mime.TypeByExtension(filepath.Ext(name))
	if t == "" {
		t = http.DetectContentType(data)
	}

	if mt, _, err := mime.ParseMediaType(t); err == nil {
		return mt
	}

	return "application/octet-stream"
}

// attach copies files into the draft, read off the main thread.
func (cm *compose) attach(paths []string) {
	go func() {
		var files []courrier.File
		var failed []string
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				failed = append(failed, filepath.Base(p))
				continue
			}

			name := filepath.Base(p)
			files = append(files, courrier.File{Name: name, MIMEType: fileType(name, data), Data: data})
		}

		cm.update(func() {
			if cm.done {
				return
			}

			for _, f := range files {
				id, err := cm.a.st.AddDraftFile(cm.d.ID, f)
				if err != nil {
					failed = append(failed, f.Name)
					continue
				}

				cm.d.Attached = append(cm.d.Attached, store.DraftFile{ID: id, File: f})
			}

			if len(failed) > 0 {
				cm.toast = "Couldn't attach " + strings.Join(failed, ", ")
			}
		})
	}()
}

func (cm *compose) pick() {
	if cm.pickFiles == nil {
		return
	}

	pickFiles := cm.pickFiles
	go func() {
		paths, err := pickFiles()
		if err != nil {
			cm.update(func() { cm.toast = "Couldn't open the files: " + err.Error() })
			return
		}

		if len(paths) > 0 {
			cm.attach(paths)
		}
	}()
}

func (cm *compose) unattach(id int64) {
	if err := cm.a.st.DeleteDraftFile(id); err != nil {
		cm.toast = "Couldn't remove it: " + err.Error()
		return
	}

	for i, f := range cm.d.Attached {
		if f.ID == id {
			cm.d.Attached = append(cm.d.Attached[:i:i], cm.d.Attached[i+1:]...)
			break
		}
	}
}

// tidy splits pasted lists into one token per address.
func tidy(tokens *[]string) {
	var out []string
	changed := false
	for _, t := range *tokens {
		if strings.ContainsAny(t, ",;") {
			if as := splitList(strings.ReplaceAll(t, ";", ",")); len(as) > 0 {
				out, changed = append(out, as...), true
				continue
			}
		}

		out = append(out, strings.TrimSpace(t))
	}

	if changed {
		*tokens = out
	}
}

func (cm *compose) view(c *ui.Context) {
	a := cm.a
	t, pal := styled(c.Theme(), a.set.Appearance, float32(a.set.FontSize))
	c.SetTheme(t)

	if cm.toast != "" {
		c.Toast(cm.toast)
		cm.toast = ""
	}

	tidy(&cm.to)
	tidy(&cm.cc)
	tidy(&cm.bcc)
	cm.track()

	if title := cm.title(); title != cm.titled && cm.setTitle != nil {
		cm.titled = title
		cm.setTitle(title)
	}

	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() { cm.sendAt(time.Time{}) })
	var froms []string
	for _, acc := range a.accounts {
		for _, id := range a.identsOf(acc) {
			froms = append(froms, identFrom(id))
		}
	}

	errs := cm.errs
	live := map[string]string{} // invalid tokens show as they are typed
	for key, list := range map[string][]string{"to": cm.to, "cc": cm.cc, "bcc": cm.bcc} {
		if bad := invalid(list); len(bad) > 0 {
			live[key] = "Not an address: " + strings.Join(bad, ", ")
		} else if errs[key] != "" && len(list) == 0 {
			live[key] = errs[key]
		}
	}

	root := ui.Column(c).Fill().Background(pal.card).Children(func() {
		titleBar(c, cm.title(), pal.card)

		ui.Form(c, func() {
			if ui.Field(c, "From", func() { ui.Select(c, &cm.from, froms).Label("From") }).Changed() {
				cm.syncFields() // swaps the signature at once
			}

			ui.Field(c.Key("to"), "To", func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.TokenField(c, &cm.to, cm.suggest).Label("To").Grow(1)

					if !cm.showBcc && ui.Button(c, "Bcc").Label("Show Bcc").Clicked() {
						cm.showBcc = true
					}
				})
			}).Error(live["to"])

			ui.Field(c.Key("cc"), "Cc", func() { ui.TokenField(c, &cm.cc, cm.suggest).Label("Cc") }).Error(live["cc"])

			if cm.showBcc {
				ui.Field(c.Key("bcc"), "Bcc", func() { ui.TokenField(c, &cm.bcc, cm.suggest).Label("Bcc") }).Error(live["bcc"])
			}

			ui.Field(c, "Subject", func() { ui.TextInput(c, &cm.d.Subject).Label("Subject") })
		}).Gap(8).Padding(14, 18, 12, 18)

		ui.Divider(c)
		cm.editor(c)
		var total int64
		for _, f := range cm.d.Attached {
			total += int64(len(f.Data))
		}

		if len(cm.d.Attached) > 0 {
			ui.Divider(c)

			ui.Row(c).Gap(8).Wrap().Padding(10, 18).Children(func() {
				for _, f := range cm.d.Attached {
					ui.Row(c.Key(f.ID)).Gap(6).AlignItems(ui.Center).Padding(4, 6, 4, 10).Radius(7).Background(t.Surface).Children(func() {
						ui.Icon(c, paperclipIcon).FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, f.Name+" · "+humanSize(int64(len(f.Data)))).FontSize(12).SingleLine()

						if ui.Button(c, "✕").Label("Remove "+f.Name).Padding(0, 4).Clicked() {
							cm.unattach(f.ID)
						}
					})
				}
			})
		}

		ui.Column(c).Gap(8).Padding(10, 18, 14, 18).Children(func() {
			if total > sizeWarning {
				notice(c, "Over 25 MB: most servers refuse messages this large.", t.Warning)
			}

			if errs["send"] != "" {
				notice(c, errs["send"], t.Danger)
			}

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				status := ""
				switch {
				case cm.saveErr != "":
					status = cm.saveErr
				case !cm.savedAt.IsZero():
					status = "Draft saved " + cm.savedAt.Format("15:04")
				}

				ui.Text(c, status).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1)

				if ui.Button(c, "Attach…").Clicked() {
					cm.pick()
				}

				if ui.PrimaryButton(c, "Send").Tooltip("Send (⌘↩)").Padding(8, 22).Clicked() {
					cm.sendAt(time.Time{})
				}

				ui.Button(c, "▾").Label("More send options").Menu(func(m *ui.Menu) {
					m.Submenu("Send later", func(sub *ui.Menu) { laterMenu(sub, &cm.later, "Send at", cm.sendAt) })
					m.Separator()

					if m.Item("Save as template").Chosen() {
						cm.saveTemplate()
					}
				})
			})
		})
	})
	if files := root.DroppedFiles(); len(files) > 0 {
		cm.attach(files)
	}

	laterDialog(c, &cm.later, 360)

	if ui.AlertDialog(c, &cm.askSubject, "Send without a subject?", "", "Cancel", "Send") == 1 {
		cm.send(true)
	}

	switch ui.AlertDialog(c, &cm.askClose, "Keep this draft?", "", "Cancel", "Discard", "Save draft") {
	case 1:
		cm.discard()
	case 2:
		cm.keep()
	}
}

// myAddrs are every account's addresses, which Reply all leaves out.
func (a *app) myAddrs() []string {
	var out []string
	for _, acc := range a.accounts {
		for _, id := range a.identsOf(acc) {
			out = append(out, id.Email)
		}
	}

	return out
}

func withFiles(d store.Draft) store.Draft {
	for _, f := range d.Files {
		d.Attached = append(d.Attached, store.DraftFile{File: f})
	}

	d.Files = nil

	return d
}

// respond opens a reply ("reply", "all") to the message shown, or a
// forward of it ("forward"). The header block gives Reply-To and
// References, the whole message the attachments forwarded: from the
// stored copy (POP3), else the server, else, offline, the cache alone.
func (a *app) respond(kind string) {
	r := a.cur
	if r == nil || len(a.accounts) == 0 {
		return
	}

	m, body := r.m, r.body
	open := func(raw []byte) {
		o := courrier.NewOriginal(m.Header, body, raw)
		d := store.Draft{AccountID: m.AccountID, ReplyPath: m.Path, ReplyUID: m.UID}
		if kind == "forward" {
			var files []courrier.File
			if raw != nil {
				files = courrier.Files(raw)
			}

			d.Draft, d.ReplyPath, d.ReplyUID = courrier.Forward(o, files), "", 0
		} else {
			d.Draft = courrier.Reply(o, a.myAddrs(), kind == "all")
		}

		for _, acc := range a.accounts {
			if acc.ID == m.AccountID {
				d.From = identFrom(replyIdent(a.identsOf(acc), m.To, m.Cc, deliveredTo(raw)))
			}
		}

		d.Text = a.sigBlock(d.From) + d.Text // above the quote
		a.openCompose(withFiles(d), true).noSigAt(0)
	}
	if raw, err := a.st.Raw(m.ID); err == nil {
		open(raw)
		return
	}

	sy := a.syncers[m.AccountID]
	whole := kind == "forward"
	if whole && !m.HasAttach {
		open(nil)
		return
	}

	if sy == nil || sy.s == nil || sy.status.Err != nil {
		if whole {
			a.toast = "Offline: the attachments aren't forwarded."
		}

		open(nil)
		return
	}

	done := func(raw []byte, err error) {
		a.update(func() {
			if err != nil {
				a.toast = "Couldn't fetch the original: " + err.Error()
				raw = nil
			}

			open(raw)
		})
	}
	if whole {
		sy.s.Raw(m.Path, m.UID, done)
	} else {
		sy.s.Header(m.Path, m.UID, done)
	}
}

// editDraft opens a message of a Drafts folder in compose: the draft it
// came from when it is still here, else the message read back. A local
// folder's message moves into the draft.
func (a *app) editDraft(m store.Message) {
	f, _ := a.folder(m.FolderID)
	if !f.Local {
		if d, ok, err := a.st.DraftByUID(m.AccountID, m.UID); err == nil && ok {
			a.openCompose(d, false)
			return
		}
	}

	load := func(raw []byte, err error) {
		var md courrier.Draft
		if err == nil {
			md, err = courrier.DraftFrom(raw)
		}

		if err != nil {
			a.toast = "Couldn't open the draft: " + err.Error()
			return
		}

		d := store.Draft{AccountID: m.AccountID, Draft: md}
		if !f.Local {
			d.ServerUID = m.UID
		}

		if a.openCompose(withFiles(d), false) != nil && f.Local {
			a.st.DeleteMessage(m.ID)
			a.reloadMail()
		}
	}
	if raw, err := a.st.Raw(m.ID); err == nil {
		load(raw, nil)
		return
	}

	s := a.syncer(m.AccountID)
	if s == nil {
		a.toast = "Couldn't open the draft: not connected to the server."
		return
	}

	s.Raw(m.Path, m.UID, func(raw []byte, err error) { a.update(func() { load(raw, err) }) })
}

// outgoingSummary reads a message's subject and recipients for the Outbox.
func outgoingSummary(raw []byte) (subj, to string) {
	fs, _ := courrier.Fields(raw)
	for _, f := range fs {
		switch strings.ToLower(f.Key) {
		case "subject":
			subj = f.Value
		case "to":
			to = f.Value
		}
	}

	return subj, to
}

func (a *app) retryOutgoing(o store.Outgoing) {
	if err := a.st.RetryOutgoing(o.ID); err != nil {
		a.toast = "Couldn't retry: " + err.Error()
	}

	if s := a.syncer(o.AccountID); s != nil {
		s.SendNow()
	}

	a.reloadMail()
}

func (a *app) reschedule(o store.Outgoing, at time.Time) {
	if err := a.st.Reschedule(o.ID, at); err != nil {
		a.toast = "Couldn't change the time: " + err.Error()
	}

	if s := a.syncer(o.AccountID); s != nil {
		s.SendNow()
	}

	a.reloadMail()
}

func (a *app) dropOutgoing(id int64) {
	o, ok, err := a.st.TakeOutgoing(id)
	switch {
	case err != nil:
		a.toast = "Couldn't delete: " + err.Error()
	case !ok:
		a.toast = "It's sent already."

	case o.DraftUID != 0:
		if s := a.syncer(o.AccountID); s != nil {
			s.DeleteDraft(o.DraftUID, func(error) {})
		}
	}

	a.reloadMail()
}

func (a *app) outboxView(c *ui.Context) {
	t := c.Theme()
	a.page(c, 12, func() {
		header(c, "Unified", "Outbox", "Messages waiting to be sent, and those scheduled. They go out by themselves once courrier is online.")

		if len(a.outbox) == 0 {
			ui.Text(c, "The outbox is empty.").TextColor(t.TextMuted)
		}

		now := time.Now()
		for _, o := range a.outbox {
			subj, to := outgoingSummary(o.Raw)
			scheduled := !o.Sent && o.LastError == "" && o.SendAfter.Sub(now) > undoWindow
			ui.Row(c.Key(o.ID)).Gap(12).AlignItems(ui.Center).Padding(12, 14).Radius(12).Background(a.pal.card).Border(1, t.Border).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, subject(subj)).FontWeight(500).SingleLine()
					ui.Text(c, "To "+to+" · from "+a.fromOf(o.AccountID)).FontSize(12).TextColor(t.TextMuted).SingleLine()

					switch {
					case o.Sent:
						ui.Text(c, "Sent. Saving it to Sent…").FontSize(12).TextColor(t.TextMuted)
					case o.LastError != "":
						ui.Text(c, o.LastError).FontSize(12).TextColor(t.Danger).Wrap()
					case scheduled:
						ui.Text(c, "Sends "+laterLabel(o.SendAfter, now)).FontSize(12).TextColor(t.Accent)
					case o.SendAfter.After(now):
						ui.Text(c, "Sending…").FontSize(12).TextColor(t.TextMuted)
					default:
						ui.Text(c, "Waiting to send").FontSize(12).TextColor(t.TextMuted)
					}
				})

				if o.Sent {
					return
				}

				if o.LastError != "" && ui.Button(c, "Retry").Clicked() {
					a.retryOutgoing(o)
				}

				if scheduled {
					if ui.Button(c, "Send now").Clicked() {
						a.retryOutgoing(o)
					}

					ui.Button(c, "Change time").Menu(func(m *ui.Menu) {
						laterMenu(m, &a.later, "Send at", func(at time.Time) { a.reschedule(o, at) })
					})
				}

				if ui.Button(c, "Edit").Clicked() {
					a.takeBack(o.ID)
				}

				if ui.Button(c, "Delete").TextColor(t.Danger).Clicked() {
					a.dropAsk, a.dropID = true, o.ID
				}
			})
		}
	})

	if ui.AlertDialog(c, &a.dropAsk, "Delete this message?", "It won't be sent.", "Cancel", "Delete") == 1 {
		a.dropOutgoing(a.dropID)
	}
}
