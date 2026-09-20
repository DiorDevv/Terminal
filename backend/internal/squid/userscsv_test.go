package squid

import (
	"bytes"
	"encoding/csv"
	"os"
	"strings"
	"testing"
	"time"
)

func (r *upRig) importCSV(text string, dry bool) (ImportReport, bool, error) {
	r.t.Helper()
	return r.up.ImportCSV(strings.NewReader(text), dry)
}

func (r *upRig) passwdText() string {
	b, _ := os.ReadFile(r.passwd)
	return string(b)
}

func rowsBy(rep ImportReport) map[string]ImportResult {
	out := map[string]ImportResult{}
	for _, r := range rep.Rows {
		out[r.Username] = r
	}
	return out
}

func TestImportCreatesAccountsWithSettingsAndGroups(t *testing.T) {
	r := newUPRig(t) // an installation with no proxy users yet

	rep, changed, err := r.importCSV(`username,password,disabled,expires,daily_quota_mb,groups,note
alice,hunter22,,2030-06-30,500,staff;sales,Front desk
bob,hunter33,yes,,0,staff,left the company
`, false)
	if err != nil || !rep.Applied || !changed {
		t.Fatalf("import: applied=%v changed=%v err=%v %+v", rep.Applied, changed, err, rep)
	}
	if rep.Created != 2 || rep.Updated != 0 || rep.Errors != 0 {
		t.Errorf("report: %+v", rep)
	}
	if !has(r.passwdText(), "alice:fake-hash-of-hunter22") || !has(r.passwdText(), "bob:fake-hash-of-hunter33") {
		t.Errorf("accounts were not created:\n%s", r.passwdText())
	}
	if !has(r.text(), "auth_param basic program") {
		t.Error("creating the first accounts must enable proxy authentication")
	}
	if !has(r.text(), "acl sqa_up_blocked proxy_auth bob") || has(r.text(), "proxy_auth alice") {
		t.Errorf("only the disabled account is blocked:\n%s", r.text())
	}

	users, _ := r.up.Users()
	alice, bob := users[0], users[1]
	wantExpiry := time.Date(2030, 7, 1, 0, 0, 0, 0, time.Local).Unix() // valid through the whole of June 30
	if alice.ExpiresAt != wantExpiry || alice.DailyQuotaMB != 500 || alice.Note != "Front desk" || len(alice.Groups) != 2 {
		t.Errorf("alice: %+v", alice)
	}
	if bob.Status != StatusDisabled || len(bob.Groups) != 1 || bob.Groups[0].Name != "staff" {
		t.Errorf("bob: %+v", bob)
	}
	groups, _ := r.up.Groups()
	if len(groups) != 2 { // staff and sales were created on the way
		t.Errorf("groups: %+v", groups)
	}
}

func TestImportUpdatesExistingAccountsWithoutTouchingWhatIsBlank(t *testing.T) {
	r := newUPRig(t, "alice")
	r.up.Update("alice", UserPatch{DailyQuotaMB: iptr(100), Note: strPtr("keep me")})
	oldPasswd := r.passwdText()

	rep, _, err := r.importCSV("username,disabled,daily_quota_mb,note\nalice,true,,\n", false)
	if err != nil || rep.Updated != 1 || rep.Created != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	u, _ := r.up.Users()
	if !u[0].Disabled || u[0].DailyQuotaMB != 100 || u[0].Note != "keep me" {
		t.Errorf("blank cells must leave a setting alone: %+v", u[0])
	}
	if r.passwdText() != oldPasswd {
		t.Error("without a password column the password must stay")
	}

	// A password for an existing account resets it.
	if _, _, err := r.importCSV("username,password\nalice,brand-new-pass\n", false); err != nil {
		t.Fatal(err)
	}
	if !has(r.passwdText(), "alice:fake-hash-of-brand-new-pass") {
		t.Errorf("the password was not reset:\n%s", r.passwdText())
	}
}

func TestImportIsAllOrNothing(t *testing.T) {
	r := newUPRig(t, "alice")
	confBefore, passwdBefore := r.text(), r.passwdText()

	rep, changed, err := r.importCSV(`username,password,disabled,expires,daily_quota_mb,groups
carol,secret-1,,,,
dave,,,,,
x,secret-2,,,,
erin,secret-3,maybe,,,
frank,secret-4,,next week,,
gina,secret-5,,,-4,
hank,secret-6,,,,bad group!
alice,,true,,,
alice,,true,,,
`, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed || rep.Applied {
		t.Fatal("one bad row must stop the whole import")
	}
	if r.text() != confBefore || r.passwdText() != passwdBefore {
		t.Error("nothing may be written")
	}
	by := rowsBy(rep)
	want := map[string]string{
		"dave":  "needs a password",
		"x":     "invalid username",
		"erin":  "not true/false",
		"frank": "not a date",
		"gina":  "daily_quota_mb",
		"hank":  "group name",
	}
	for name, sub := range want {
		got := by[name]
		if got.Action != "error" || !strings.Contains(got.Error, sub) {
			t.Errorf("%s: %+v, want an error containing %q", name, got, sub)
		}
	}
	if by["carol"].Action != "create" {
		t.Errorf("a good row is reported as it would be applied: %+v", by["carol"])
	}
	dupErrors := 0
	for _, row := range rep.Rows {
		if row.Username == "alice" && row.Action == "error" && strings.Contains(row.Error, "already appears on line") {
			dupErrors++
		}
	}
	if dupErrors != 1 {
		t.Errorf("the second alice row is a duplicate: %+v", rep.Rows)
	}
	for i := 1; i < len(rep.Rows); i++ {
		if rep.Rows[i].Line < rep.Rows[i-1].Line {
			t.Error("results are reported in file order")
		}
	}
	if rep.Rows[0].Line != 2 {
		t.Errorf("line numbers count the header as line 1, first row is line %d", rep.Rows[0].Line)
	}
}

func TestImportDryRunChangesNothing(t *testing.T) {
	r := newUPRig(t, "alice")
	confBefore, passwdBefore := r.text(), r.passwdText()
	rep, changed, err := r.importCSV("username,password,disabled\nalice,,true\nzed,pass-zed,\n", true)
	if err != nil || changed || rep.Applied || !rep.DryRun {
		t.Fatalf("%+v %v %v", rep, changed, err)
	}
	if rep.Created != 1 || rep.Updated != 1 {
		t.Errorf("a dry run reports what would happen: %+v", rep)
	}
	if r.text() != confBefore || r.passwdText() != passwdBefore {
		t.Error("a dry run writes nothing")
	}
	if u, _ := r.up.Users(); u[0].Disabled {
		t.Error("a dry run changes no settings")
	}
}

func TestImportRejectsBadFilesOutright(t *testing.T) {
	r := newUPRig(t, "alice")
	cases := map[string]string{
		"empty":               "",
		"header only":         "username,password\n",
		"no username column":  "name,password\nalice,x\n",
		"unknown column":      "username,shell\nalice,/bin/sh\n",
		"duplicate column":    "username,username\nalice,alice\n",
		"broken quoting":      "username,note\nalice,\"unterminated\n",
		"too many rows":       "username,password\n" + strings.Repeat("user_x,abcdef\n", maxImportRows+1),
		"larger than the cap": "username,note\nalice," + strings.Repeat("x", maxImportBytes+10) + "\n",
	}
	for name, text := range cases {
		var pe *PolicyError
		if _, _, err := r.importCSV(text, false); err == nil || !asPolicy(err, &pe) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func asPolicy(err error, target **PolicyError) bool {
	pe, ok := err.(*PolicyError)
	if ok {
		*target = pe
	}
	return ok
}

func TestImportToleratesExcelFiles(t *testing.T) {
	r := newUPRig(t, "alice")
	// A byte order mark, CRLF line endings, a blank line, odd header case and
	// stray spaces: what Excel produces.
	text := "\ufeffUsername , Disabled\r\n\r\nalice , yes\r\n"
	rep, _, err := r.importCSV(text, false)
	if err != nil || rep.Updated != 1 || rep.Errors != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if u, _ := r.up.Users(); !u[0].Disabled {
		t.Error("the row was not applied")
	}
}

func TestExpiryCellFormats(t *testing.T) {
	day, err := parseExpiry("2031-01-15")
	if err != nil || day != time.Date(2031, 1, 16, 0, 0, 0, 0, time.Local).Unix() {
		t.Errorf("a date means valid through that whole day: %d %v", day, err)
	}
	ts, err := parseExpiry("2031-01-15T10:30:00Z")
	if err != nil || ts != time.Date(2031, 1, 15, 10, 30, 0, 0, time.UTC).Unix() {
		t.Errorf("an RFC 3339 time is taken as written: %d %v", ts, err)
	}
	for _, bad := range []string{"tomorrow", "15.01.2031", "2031-13-40", "31/12/2030"} {
		if _, err := parseExpiry(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestExportHasNoPasswordsAndDefusesFormulas(t *testing.T) {
	r := newUPRig(t, "alice", "-dash")
	r.up.Update("alice", UserPatch{Note: strPtr(`=HYPERLINK("http://evil.example","x")`), DailyQuotaMB: iptr(20)})
	g, _ := r.up.CreateGroup("staff")
	r.up.SetMembers(g.ID, []string{"alice"})

	var buf bytes.Buffer
	if err := r.up.ExportCSV(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "hash") || strings.Contains(out, "apr1") {
		t.Errorf("an export must never contain password material:\n%s", out)
	}
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || len(recs) != 3 {
		t.Fatalf("%v %v", recs, err)
	}
	if strings.Join(recs[0], ",") != "username,status,disabled,expires,daily_quota_mb,groups,note" {
		t.Errorf("header: %v", recs[0])
	}
	byName := map[string][]string{}
	for _, rec := range recs[1:] {
		byName[rec[0]] = rec
	}
	if got := byName["alice"][6]; !strings.HasPrefix(got, "'=") {
		t.Errorf("a note starting with = must not be a formula: %q", got)
	}
	if byName["'-dash"] == nil {
		t.Errorf("a name starting with - is defused too: %v", byName)
	}
	if byName["alice"][5] != "staff" || byName["alice"][4] != "20" {
		t.Errorf("alice: %v", byName["alice"])
	}
}

func TestExportThenImportChangesNothing(t *testing.T) {
	r := newUPRig(t, "alice", "-dash", "carol")
	r.up.Update("alice", UserPatch{Note: strPtr("=1+1"), DailyQuotaMB: iptr(20), ExpiresAt: i64ptr(r.clock.Add(48 * time.Hour).Unix())})
	r.up.Update("-dash", UserPatch{Disabled: bptr(true)})
	g, _ := r.up.CreateGroup("staff")
	r.up.SetMembers(g.ID, []string{"alice", "carol"})
	before, _ := r.up.Users()

	var buf bytes.Buffer
	r.up.ExportCSV(&buf)
	rep, _, err := r.importCSV(buf.String(), false)
	if err != nil || rep.Errors != 0 || rep.Created != 0 || rep.Updated != 3 {
		t.Fatalf("re-importing an export must work: %+v %v", rep, err)
	}
	after, _ := r.up.Users()
	for i := range before {
		b, a := before[i], after[i]
		if b.Username != a.Username || b.Disabled != a.Disabled || b.ExpiresAt != a.ExpiresAt ||
			b.DailyQuotaMB != a.DailyQuotaMB || b.Note != a.Note || len(b.Groups) != len(a.Groups) {
			t.Errorf("round trip changed %s:\n before %+v\n after  %+v", b.Username, b, a)
		}
	}
}
