package sshconf

import (
	"strings"
	"testing"
)

func testBlock() Block {
	return Block{
		ID:           "ws-1",
		Alias:        "unkindled-dev-box",
		HostName:     "203.0.113.9",
		User:         "ubuntu",
		IdentityFile: "/home/u/.ssh/unkindled/dev-box",
	}
}

func TestBuild(t *testing.T) {
	block, err := Build(testBlock())
	if err != nil {
		t.Fatal(err)
	}
	want := "# unkindled:ws-1\n" +
		"Host unkindled-dev-box\n" +
		"  HostName 203.0.113.9\n" +
		"  User ubuntu\n" +
		"  IdentityFile /home/u/.ssh/unkindled/dev-box\n" +
		"  IdentitiesOnly yes\n" +
		"# end unkindled:ws-1\n"
	if block != want {
		t.Fatalf("Build() =\n%q\nwant\n%q", block, want)
	}
}

func TestBuildPort(t *testing.T) {
	b := testBlock()
	b.Port = 2222
	block, err := Build(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, "  Port 2222\n  IdentityFile ") {
		t.Fatalf("unexpected block:\n%s", block)
	}
}

func TestBuildRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Block)
		want   string
	}{
		{name: "id newline", mutate: func(b *Block) { b.ID = "ws\n1" }, want: "newline"},
		{name: "alias wildcard", mutate: func(b *Block) { b.Alias = "*" }, want: "alias"},
		{name: "alias space", mutate: func(b *Block) { b.Alias = "bad alias" }, want: "alias"},
		{name: "host space", mutate: func(b *Block) { b.HostName = "bad host" }, want: "host"},
		{name: "user space", mutate: func(b *Block) { b.User = "bad user" }, want: "user"},
		{name: "identity newline", mutate: func(b *Block) { b.IdentityFile = "/tmp/key\nHost *" }, want: "identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := testBlock()
			tt.mutate(&b)
			_, err := Build(b)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Build() error = %v", err)
			}
		})
	}
}

func TestUpsertAppendsAndIsIdempotent(t *testing.T) {
	original := "# keep this\nHost github.com\n  User git\n"
	block, err := Build(testBlock())
	if err != nil {
		t.Fatal(err)
	}
	updated, changed, err := Upsert(original, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if !strings.HasPrefix(updated, original) {
		t.Fatalf("existing content was not preserved:\n%q", updated)
	}
	if !strings.Contains(updated, "\n\n"+block) {
		t.Fatalf("block not appended with a blank line:\n%s", updated)
	}
	again, changed, err := Upsert(updated, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	if changed || again != updated {
		t.Fatalf("Upsert was not idempotent:\n%s", again)
	}
}

func TestUpsertReplacesManagedBlock(t *testing.T) {
	oldBlock, err := Build(Block{
		ID: "ws-1", Alias: "old", HostName: "198.51.100.1", User: "root", IdentityFile: "/old/key",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := "# top\n\n" + oldBlock + "\n# bottom\n"
	updated, changed, err := Upsert(original, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(updated, "Host old") || strings.Contains(updated, "/old/key") {
		t.Fatalf("old block still present:\n%s", updated)
	}
	if strings.Count(updated, "# unkindled:ws-1") != 1 {
		t.Fatalf("duplicate marker:\n%s", updated)
	}
	if !strings.HasPrefix(updated, "# top\n\n# unkindled:ws-1\n") || !strings.HasSuffix(updated, "# end unkindled:ws-1\n\n# bottom\n") {
		t.Fatalf("surrounding content changed:\n%q", updated)
	}
}

func TestUpsertPreservesCRLF(t *testing.T) {
	original := "# keep\r\nHost github.com\r\n  User git\r\n"
	updated, changed, err := Upsert(original, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if !strings.HasPrefix(updated, original) {
		t.Fatalf("CRLF prefix changed:\n%q", updated)
	}
	if strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatal("mixed line endings produced")
	}
	again, changed, err := Upsert(updated, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	if changed || again != updated {
		t.Fatal("CRLF Upsert was not idempotent")
	}
}

func TestRemove(t *testing.T) {
	original := "# keep\nHost github.com\n  User git\n"
	added, _, err := Upsert(original, testBlock())
	if err != nil {
		t.Fatal(err)
	}
	removed, changed, err := Remove(added, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected removal")
	}
	if removed != original {
		t.Fatalf("Remove() =\n%q\nwant\n%q", removed, original)
	}
	again, changed, err := Remove(removed, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if changed || again != removed {
		t.Fatal("Remove of missing block changed content")
	}
}

func TestMalformedBlocks(t *testing.T) {
	content := "# unkindled:ws-1\nHost broken\n"
	if _, _, err := Upsert(content, testBlock()); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Upsert error = %v", err)
	}
	if _, _, err := Remove(content, "ws-1"); err == nil {
		t.Fatal("Remove should reject malformed block")
	}
	if _, _, err := Find(content, "ws-1"); err == nil {
		t.Fatal("Find should reject malformed block")
	}

	block, err := Build(testBlock())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := block + "\n" + block
	if _, _, err := Upsert(duplicate, testBlock()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Upsert duplicate error = %v", err)
	}
}

func TestFindAndIDs(t *testing.T) {
	block, err := Build(testBlock())
	if err != nil {
		t.Fatal(err)
	}
	content := "# intro\n\n" + block + "\n# trailing\n"
	found, ok, err := Find(content, "ws-1")
	if err != nil || !ok {
		t.Fatalf("Find() = %+v, %v, %v", found, ok, err)
	}
	if found.Alias != "unkindled-dev-box" || found.HostName != "203.0.113.9" || found.User != "ubuntu" ||
		found.IdentityFile != "/home/u/.ssh/unkindled/dev-box" || found.ID != "ws-1" {
		t.Fatalf("parsed block = %+v", found)
	}
	if _, ok, err := Find(content, "missing"); err != nil || ok {
		t.Fatalf("Find(missing) = %v, %v", ok, err)
	}
	ids := IDs(content + "# unkindled:ws-2\n")
	if len(ids) != 2 || ids[0] != "ws-1" || ids[1] != "ws-2" {
		t.Fatalf("IDs() = %#v", ids)
	}
}

func TestValidateID(t *testing.T) {
	if err := ValidateID(""); err == nil {
		t.Fatal("empty ID accepted")
	}
	if err := ValidateID("a\nb"); err == nil {
		t.Fatal("newline ID accepted")
	}
	if err := ValidateID("ws-1"); err != nil {
		t.Fatal(err)
	}
}
