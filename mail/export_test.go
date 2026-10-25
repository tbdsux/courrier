package mail

import "context"

// SMTPServer is smtpServer for the tests of package mail_test.
var SMTPServer = smtpServer

// FakeServer is fakeServer for the tests of package mail_test.
var FakeServer = fakeServer

// SyncOnce syncs one folder over a connection of its own, as a Syncer does.
func SyncOnce(e Endpoint, cache Cache, account int64, path string) error {
	ic, err := dialIMAP(context.Background(), e)
	if err != nil {
		return err
	}

	defer ic.close()
	f := Folder{Path: path}
	_, err = ic.syncFolder(cache, account, &f)
	return err
}
