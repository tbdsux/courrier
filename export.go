package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	courrier "courrier/mail"
	"courrier/store"
)

// keepsAll tells the folders the cache holds whole, whose mail exports
// from here and imports straight in: POP3 accounts' and local ones.
func (a *app) keepsAll(f store.Folder) bool { return f.Local || a.isPOP3(f.AccountID) }

// fileName makes s fit for a file's name on every system.
func fileName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < ' ' {
			return '_'
		}
		return r
	}, s)
}

// exportFolder writes a folder's mail to an mbox at path: the server's for
// an IMAP folder, else the copies kept here. done runs on the main thread
// with how many it wrote; a failed export leaves no file.
func (a *app) exportFolder(f store.Folder, path string, done func(n int, err error)) {
	file, err := os.Create(path)
	if err != nil {
		done(0, err)
		return
	}
	n := 0
	each := func(raw []byte) error { n++; return courrier.WriteMbox(file, raw) }
	finish := func(err error) {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
		}
		a.update(func() { done(n, err) })
	}
	if a.keepsAll(f) {
		st := a.st
		go func() { finish(st.EachRaw(f.ID, each)) }()
		return
	}
	s := a.syncer(f.AccountID)
	if s == nil {
		finish(errors.New("not connected to the server"))
		return
	}
	s.Export(f.Path, each, finish)
}

func exported(n int, path string) string {
	return fmt.Sprintf("Exported %d messages to %s", n, filepath.Base(path))
}

// askExport asks where to export a folder, then does.
func (a *app) askExport(f store.Folder, name string) {
	if a.saveFile == nil {
		return
	}
	saveFile := a.saveFile
	go func() {
		path, err := saveFile("Export as mbox", fileName(name)+".mbox")
		a.update(func() {
			switch {
			case err != nil:
				a.toast = "Couldn't export: " + err.Error()
			case path != "":
				a.toast = "Exporting…"
				a.exportFolder(f, path, func(n int, err error) {
					if a.toast = exported(n, path); err != nil {
						a.toast = "Couldn't export: " + err.Error()
					}
				})
			}
		})
	}()
}

// askExportAccount asks for a folder to export each of an account's
// folders to, an mbox each, then does.
func (a *app) askExportAccount(account int64) {
	if a.pickDir == nil {
		return
	}
	pickDir := a.pickDir
	go func() {
		dir, err := pickDir("Export every folder to")
		a.update(func() {
			switch {
			case err != nil:
				a.toast = "Couldn't export: " + err.Error()
			case dir != "":
				var fs []store.Folder
				for _, f := range a.folders {
					if f.AccountID == account && !f.NoSelect {
						fs = append(fs, f)
					}
				}
				a.toast = "Exporting…"
				a.exportAll(fs, dir, 0)
			}
		})
	}()
}

func (a *app) exportAll(fs []store.Folder, dir string, total int) {
	if len(fs) == 0 {
		a.toast = exported(total, dir)
		return
	}
	f := fs[0]
	a.exportFolder(f, filepath.Join(dir, fileName(f.Path)+".mbox"), func(n int, err error) {
		if err != nil {
			a.toast = "Couldn't export " + f.Path + ": " + err.Error()
			return
		}
		a.exportAll(fs[1:], dir, total+n)
	})
}

// askImport asks for an mbox to import into a folder, then does: into the
// cache for the folders it holds whole, else queued for the server.
func (a *app) askImport(f store.Folder) {
	if a.openFile == nil {
		return
	}
	openFile, st, local := a.openFile, a.st, a.keepsAll(f)
	go func() {
		path, err := openFile("Import mbox")
		if err != nil || path == "" {
			if err != nil {
				a.update(func() { a.toast = "Couldn't import: " + err.Error() })
			}
			return
		}
		n, err := importMbox(st, f, path, local)
		a.update(func() {
			if s := a.syncer(f.AccountID); s != nil && !local {
				s.Refresh(f.Path) // which stores what is queued first
			}
			a.reloadMail()
			switch {
			case err != nil:
				a.toast = fmt.Sprintf("Couldn't import all of it, %d messages in: %v", n, err)
			case local:
				a.toast = fmt.Sprintf("Imported %d messages", n)
			default:
				a.toast = fmt.Sprintf("Importing %d messages: they show as the server takes them", n)
			}
		})
	}()
}

func importMbox(st *store.Store, f store.Folder, path string, local bool) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	n := 0
	err = courrier.ReadMbox(file, func(raw []byte) error {
		add := st.QueueAppend
		if local {
			add = st.Import
		}
		if err := add(f.AccountID, f.Path, raw); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}
