package domain

import "testing"

func TestSlugAndSnake(t *testing.T) {
	tests := []struct{ in, slug, snake string }{
		{"Amber Finch", "amber-finch", "amber_finch"},
		{"feat/login.page:2", "feat-login-page-2", "feat_login_page_2"},
		{"--weird__", "weird", "weird"},
		{"ÄÖ", "", ""},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.slug {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.slug)
		}
		if got := Snake(tt.in); got != tt.snake {
			t.Errorf("Snake(%q) = %q, want %q", tt.in, got, tt.snake)
		}
	}
}

func TestDefaultStackNameIsStableAndVaries(t *testing.T) {
	a := DefaultStackName("/src/shop")
	if a != DefaultStackName("/src/shop") {
		t.Fatal("same path gave different names")
	}
	if a == DefaultStackName("/src/shop-worktree") {
		t.Fatalf("different paths both gave %q", a)
	}
	if Slug(a) != a {
		t.Fatalf("name %q is not slug-safe", a)
	}
}

func TestIdentity(t *testing.T) {
	id, err := NewIdentity("My Shop", "Amber.Finch")
	if err != nil {
		t.Fatal(err)
	}
	if id.Key() != "my-shop--amber-finch" {
		t.Fatalf("got key %q", id.Key())
	}
	back, err := ParseKey(id.Key())
	if err != nil || back != id {
		t.Fatalf("ParseKey = %v, %v", back, err)
	}
	if _, err := NewIdentity("!!!", "x"); err == nil {
		t.Fatal("expected error for unusable project name")
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[32mready\x1b[0m on \x1b]8;;http://x\x07link\x1b]8;;\x07"
	if got := StripANSI(in); got != "ready on link" {
		t.Fatalf("StripANSI = %q", got)
	}
}

func TestRunOutput(t *testing.T) {
	banner := func(pid int) string {
		return BannerPrefix + "demo/a db " + RunMarker(pid) + " -- sleep 2; echo listening on 1"
	}
	tests := []struct {
		name    string
		history string
		want    string
	}{
		{
			name:    "skips the banner, which quotes the command",
			history: banner(7) + "\nbooting",
			want:    "booting",
		},
		{
			name:    "drops earlier runs",
			history: banner(6) + "\nlistening on 1\n" + banner(7) + "\n\x1b[32mbooting\x1b[0m",
			want:    "booting",
		},
		{
			name:    "nothing before this run's banner",
			history: banner(6) + "\nlistening on 1",
			want:    "",
		},
		{
			name:    "banner trimmed by the history limit",
			history: "still booting\nlistening on 1",
			want:    "still booting\nlistening on 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RunOutput(tt.history, 7); got != tt.want {
				t.Errorf("RunOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}
