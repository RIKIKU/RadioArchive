package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The audit log (SPEC.md R12, R54; ADR 0029): every admin change, written
// by Audited in the same transaction as the change, so a change can't
// commit without its entry and an entry can't exist without its change. It
// holds names, because admins read it; nothing here logs.

// How an admin change was made: from how the request was signed in, never
// from what the client says.
const (
	// ViaBrowser: a browser's session cookie.
	ViaBrowser = "browser"
	// ViaModule: the PowerShell module's bearer token (issue #116).
	ViaModule = "module"
	// ViaImport: the setup import (issue #115).
	ViaImport = "import"
)

// The kinds of thing an admin changes.
const (
	AuditSystem     = "system"
	AuditTalkgroup  = "talkgroup"
	AuditUnit       = "unit"
	AuditGroup      = "group"
	AuditTag        = "tag"
	AuditAttachment = "attachment"
	AuditRecorder   = "recorder"
	AuditRetention  = "retention"
	AuditAccount    = "account"
)

// AuditKinds are the kinds, in the order the Audit log page lists them.
var AuditKinds = []string{AuditSystem, AuditTalkgroup, AuditUnit, AuditGroup, AuditTag, AuditAttachment,
	AuditRecorder, AuditRetention, AuditAccount}

// What an entry says happened to its thing.
const (
	Created = "created"
	Changed = "changed"
	Deleted = "deleted"
)

// Actor is the admin making a change, and how: who and how in each entry.
type Actor struct {
	// Issuer and Subject are the admin's account.
	Issuer, Subject string
	// Name and Email are as they were at the change.
	Name, Email string
	// Via is ViaBrowser, ViaModule or ViaImport.
	Via string
}

// ErrNoActor: an admin change without an admin, or without how it was
// made. Nothing is changed.
var ErrNoActor = errors.New("store: an audited change needs its admin and how it was made")

func (a Actor) check() error {
	if a.Subject == "" || (a.Via != ViaBrowser && a.Via != ViaModule && a.Via != ViaImport) {
		return ErrNoActor
	}
	return nil
}

// Change is one thing's change, as Audit.Record takes it. Before and After
// are the thing as the admin API shows it (a struct that marshals to a JSON
// object, without its timestamps): Before nil is a creation, After nil a
// deletion.
type Change struct {
	Kind string
	// Thing is which one, a stable key: "1" (system 1), "1/1001" (its
	// talkgroup 1001), "1/5100-5199" (a range of its units), a group's or
	// tag's ID, "Bench/ex" (an attachment), a recorder's name, the
	// account's subject; "" for retention.
	Thing string
	// Label is the name the thing goes by, for people: "EX 1001 DISP N".
	Label         string
	Before, After any
}

// FieldChange is one field's change: null before for a field a creation
// set, null after for one a deletion took.
type FieldChange struct {
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// Audit records the changes of one audited transaction.
type Audit struct {
	ctx context.Context
	q   *db.Queries
	by  Actor
	n   int
}

// Lock waits for any other audited change of the thing to finish, so the
// "before" read after it is the thing as this change finds it. A change
// locks its thing before reading it.
func (au *Audit) Lock(kind, thing string) error {
	return au.q.LockAuditThing(au.ctx, db.LockAuditThingParams{Kind: kind, Thing: thing})
}

// Record writes c's entry, in the transaction. A change that changes no
// field writes nothing: nothing happened.
func (au *Audit) Record(c Change) error {
	action, fields, err := diff(c.Before, c.After)
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := au.q.InsertAuditEntry(au.ctx, db.InsertAuditEntryParams{Issuer: au.by.Issuer, Subject: au.by.Subject,
		AdminName: au.by.Name, AdminEmail: au.by.Email, Via: au.by.Via, Kind: c.Kind, Thing: c.Thing,
		Label: c.Label, Action: action, Changes: b}); err != nil {
		return err
	}
	au.n++
	return nil
}

// Audited runs f in one transaction, as by: f makes an admin change with q
// and records it with au. f's error, or a failure writing an entry, rolls
// back the change and every entry with it. by must name the admin and how
// the change was made, or nothing is run.
func (s *Store) Audited(ctx context.Context, by Actor, f func(q *db.Queries, au *Audit) error) error {
	if err := by.check(); err != nil {
		return err
	}
	return s.inTx(ctx, func(q *db.Queries) error {
		return f(q, &Audit{ctx: ctx, q: q, by: by})
	})
}

// field is one JSON object member, in its order.
type field struct {
	name string
	raw  json.RawMessage
}

// fields are v's JSON object members in order; nil for nil or null.
func fields(v any) ([]field, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(b, []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("store: an audited thing must be a JSON object, not %s", b)
	}
	var out []field
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, field{name: t.(string), raw: raw})
	}
	return out, nil
}

var null = json.RawMessage("null")

// diff is what changed from before to after, field by field, in after's
// order (then before's, for fields after lacks).
func diff(before, after any) (string, []FieldChange, error) {
	bf, err := fields(before)
	if err != nil {
		return "", nil, err
	}
	af, err := fields(after)
	if err != nil {
		return "", nil, err
	}
	var out []FieldChange
	switch {
	case bf == nil && af == nil:
		return "", nil, errors.New("store: an audited change needs a before or an after")
	case bf == nil:
		for _, f := range af {
			out = append(out, FieldChange{Field: f.name, Before: null, After: f.raw})
		}
		return Created, out, nil
	case af == nil:
		for _, f := range bf {
			out = append(out, FieldChange{Field: f.name, Before: f.raw, After: null})
		}
		return Deleted, out, nil
	}
	was := map[string]json.RawMessage{}
	for _, f := range bf {
		was[f.name] = f.raw
	}
	seen := map[string]bool{}
	for _, f := range af {
		seen[f.name] = true
		b, ok := was[f.name]
		if !ok {
			b = null
		}
		if !sameJSON(b, f.raw) {
			out = append(out, FieldChange{Field: f.name, Before: b, After: f.raw})
		}
	}
	for _, f := range bf {
		if !seen[f.name] && !sameJSON(f.raw, null) {
			out = append(out, FieldChange{Field: f.name, Before: f.raw, After: null})
		}
	}
	return Changed, out, nil
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// AuditEntry is one entry, as the read API shows it.
type AuditEntry struct {
	ID      int64
	At      time.Time
	By      Actor
	Kind    string
	Thing   string
	Label   string
	Action  string
	Changes []FieldChange
}

// AuditFilter narrows the entries; zero values don't.
type AuditFilter struct {
	// Subject is the admin's account.
	Subject     string
	Kind, Thing string
	// Since and Until bound when, Until exclusive.
	Since, Until time.Time
	// AfterAt and AfterID are the cursor: the last entry of the page
	// before. Zero for the first page.
	AfterAt time.Time
	AfterID int64
	// Limit is the page's size.
	Limit int
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return ts(t)
}

// AuditEntries are the entries f selects, newest first. It writes nothing:
// reading the audit log isn't in it.
func (s *Store) AuditEntries(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	p := db.ListAuditEntriesParams{Subject: optString(f.Subject), Kind: optString(f.Kind), Thing: optString(f.Thing),
		Since: optTime(f.Since), Until: optTime(f.Until), PageSize: int32(f.Limit)}
	if !f.AfterAt.IsZero() {
		p.AfterAt, p.AfterID = ts(f.AfterAt), &f.AfterID
	}
	rows, err := s.ListAuditEntries(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("read the audit log: %s", SafeError(err))
	}
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		e := AuditEntry{ID: r.ID, At: r.At.Time, Kind: r.Kind, Thing: r.Thing, Label: r.Label, Action: r.Action,
			By: Actor{Issuer: r.Issuer, Subject: r.Subject, Name: r.AdminName, Email: r.AdminEmail, Via: r.Via}}
		if err := json.Unmarshal(r.Changes, &e.Changes); err != nil {
			return nil, fmt.Errorf("read the audit log: entry %d: %T", r.ID, err)
		}
		out = append(out, e)
	}
	return out, nil
}
