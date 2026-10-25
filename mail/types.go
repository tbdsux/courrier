package mail

import "time"

// Folder is a mailbox on the server (or, for POP3, one kept only locally).
type Folder struct {
	Path        string // full server name, e.g. "Projects/Alpha"
	Delim       string
	Special     string // inbox|drafts|sent|archive|junk|trash|""
	NoSelect    bool
	Local       bool
	UIDValidity uint32
	UIDNext     uint32
	Unread      int
	Total       int
}

// Flags are the message flags courrier shows.
type Flags struct{ Seen, Flagged, Answered bool }

// Header is what the message list shows of a message.
type Header struct {
	UID       uint32
	MessageID string
	Refs      []string // References, then In-Reply-To: the messages it answers
	FromName  string
	FromAddr  string
	To, Cc    string // "Name <a@b>, …" as shown
	Subject   string
	Date      time.Time
	Flags
	Size      int64
	HasAttach bool
	Snippet   string
}

// Attachment is a part of a message saved as a file.
type Attachment struct {
	Part      string // IMAP section, e.g. "2.1"
	Filename  string
	MIMEType  string
	Size      int64
	ContentID string
}

// Body is a parsed message's content.
type Body struct {
	Text, HTML  string
	Attachments []Attachment
}

// Cache is where a Syncer writes what it fetches. store.Store implements it.
type Cache interface {
	// SetFolders replaces an account's server folders with fs; a folder
	// whose UIDValidity changed loses its cached messages.
	SetFolders(account int64, fs []Folder) error
	// UpdateFolder updates one folder's counts and UIDValidity the same way.
	UpdateFolder(account int64, f Folder) error
	// UIDRange returns the oldest and newest UID cached for a folder, 0 when none.
	UIDRange(account int64, path string) (oldest, newest uint32, err error)
	AddHeaders(account int64, path string, hs []Header) error
	// SyncFlags sets the flags of the cached UIDs from..to and deletes the
	// ones flags lacks, which the server no longer has.
	SyncFlags(account int64, path string, from, to uint32, flags map[uint32]Flags) error
	// ChangeFlags sets the flags of the cached UIDs flags has, and, unless
	// present is nil, deletes the cached UIDs from..to it lacks.
	ChangeFlags(account int64, path string, flags map[uint32]Flags, from, to uint32, present map[uint32]bool) error
	// SyncState returns what SetSyncState kept of a folder, 0 when nothing:
	// its HIGHESTMODSEQ and number of messages at the last sync. A new
	// UIDValidity forgets it.
	SyncState(account int64, path string) (modseq uint64, exists uint32, err error)
	SetSyncState(account int64, path string, modseq uint64, exists uint32) error
	SetBody(account int64, path string, uid uint32, b Body) error
	SeenUIDLs(account int64) (map[string]bool, error)
	// AddPOP3 stores a downloaded message in the Inbox and remembers its UIDL.
	AddPOP3(account int64, uidl string, h Header, b Body, raw []byte) error
	// AddLocal stores a message in a local folder of the account, by its
	// special use, as POP3 accounts keep their Sent mail.
	AddLocal(account int64, special string, raw []byte) error

	// Outbox returns the account's messages waiting to be sent, or for
	// their Sent copy.
	Outbox(account int64) ([]Outgoing, error)
	// Claim reserves a message for sending until until, unless it is
	// reserved already or taken back; it reports whether it got it.
	Claim(id int64, until time.Time) (bool, error)
	// SentOK records that SMTP accepted a message, which never sends again.
	SentOK(id int64) error
	// SendFailed records why a message wasn't sent; it is retried later.
	SendFailed(id int64, msg string) error
	// CopyStored deletes a sent message whose Sent copy is stored.
	CopyStored(id int64) error

	// Actions returns the account's actions waiting for the server, oldest first.
	Actions(account int64) ([]Action, error)
	// ActionDone records that the server did an action; for a move, dest
	// are the UIDs the messages got, kept to undo it (none without UIDPLUS).
	ActionDone(id int64, dest []uint32) error
	// ActionFailed records that the server refused an action, and drops it
	// the second time; it reports whether it did.
	ActionFailed(id int64, msg string) (dropped bool, err error)
	// ActionRaw returns the message an append action stores.
	ActionRaw(id int64) ([]byte, error)
	// CachedUIDs returns the UIDs cached for a folder.
	CachedUIDs(account int64, path string) (map[uint32]bool, error)
}

// Action is a change made in the cache, waiting to be made on the server.
type Action struct {
	ID   int64
	Op   string // seen|unseen|flag|unflag|move|expunge|append
	Path string // the folder the UIDs are in
	UIDs []uint32
	Dest string // move: the folder they go to, created when missing
}

// Outgoing is a message of the outbox.
type Outgoing struct {
	ID        int64
	Raw       []byte   // the MIME message, without Bcc
	Rcpts     []string // envelope recipients, Bcc included
	SendAfter time.Time
	Attempts  int
	LastError string
	Sent      bool   // SMTP took it; only its Sent copy is left to store
	ReplyPath string // the message it answers, flagged \Answered once sent
	ReplyUID  uint32
	DraftUID  uint32 // its draft's copy in the Drafts folder, deleted once sent
}
