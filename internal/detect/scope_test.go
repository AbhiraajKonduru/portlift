package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func p(slash string) string { return filepath.FromSlash(slash) }

func TestClassify(t *testing.T) {
	root := p("/code/shop")
	tests := []struct {
		name           string
		dir, cwd, root string
		want           Tier
	}{
		{"same folder", "/code/shop/apps/web", "/code/shop/apps/web", root, TierBelow},
		{"below", "/code/shop/apps/web", "/code/shop", root, TierBelow},
		{"above, at repo root", "/code/shop", "/code/shop/apps/web/src", root, TierAbove},
		{"above, inside repo", "/code/shop/apps/web", "/code/shop/apps/web/src", root, TierAbove},
		{"sibling", "/code/shop/apps/api", "/code/shop/apps/web", root, TierSibling},
		{"sibling with shared name prefix", "/code/shop/apps/web2", "/code/shop/apps/web", root, TierSibling},
		{"another repo", "/code/blog", "/code/shop/apps/web", root, TierOutside},
		{"above the repo root", "/code", "/code/shop/apps/web", root, TierOutside},
		{"no repo, below", "/code/shop", "/code", "", TierBelow},
		{"no repo, above", "/code", "/code/shop", "", TierOutside},
		{"no repo, shared name prefix", "/code/shop2", "/code/shop", "", TierOutside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.root
			if root != "" {
				root = p(root)
			}
			if got := classify(p(tt.dir), p(tt.cwd), root); got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %d, want %d", tt.dir, tt.cwd, tt.root, got, tt.want)
			}
		})
	}
}

func TestWithinFoldsCaseWhereTheFilesystemDoes(t *testing.T) {
	got := within(p("/Code/Shop/web"), p("/code/shop"))
	if got != foldCase {
		t.Errorf("within with differing case = %v, want %v", got, foldCase)
	}
}

func TestWithinFilesystemRoot(t *testing.T) {
	if !within(p("/code"), p("/")) {
		t.Error("a folder should be within the filesystem root")
	}
}

func TestRepoRoot(t *testing.T) {
	base := canonical(t.TempDir())

	repo := filepath.Join(base, "repo")
	deep := filepath.Join(repo, "a", "b")
	mustMkdir(t, deep)
	mustMkdir(t, filepath.Join(repo, ".git"))
	if got := repoRoot(deep); got != repo {
		t.Errorf("repoRoot with a .git folder = %q, want %q", got, repo)
	}

	// Worktrees and submodules mark their root with a .git file.
	worktree := filepath.Join(repo, "a", "worktree")
	inner := filepath.Join(worktree, "src")
	mustMkdir(t, inner)
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repoRoot(inner); got != worktree {
		t.Errorf("repoRoot with a .git file = %q, want %q", got, worktree)
	}
}

func TestCanonicalIgnoresTrailingSeparator(t *testing.T) {
	dir := canonical(t.TempDir())
	if got := canonical(dir + string(filepath.Separator)); got != dir {
		t.Errorf("canonical with trailing separator = %q, want %q", got, dir)
	}
}

func TestAuto(t *testing.T) {
	s := func(port int, tier Tier) Server { return Server{Port: port, Tier: tier} }
	tests := []struct {
		name    string
		servers []Server
		want    int // port picked, 0 when the user must choose
	}{
		{"nothing", nil, 0},
		{"one below", []Server{s(3000, TierBelow)}, 3000},
		{"one above", []Server{s(3000, TierAbove)}, 3000},
		{"below beats above", []Server{s(3000, TierBelow), s(4000, TierAbove)}, 3000},
		{"above beats sibling", []Server{s(3000, TierAbove), s(4000, TierSibling)}, 3000},
		{"two below tie", []Server{s(3000, TierBelow), s(4000, TierBelow)}, 0},
		{"lone sibling needs confirmation", []Server{s(3000, TierSibling)}, 0},
		{"two siblings", []Server{s(3000, TierSibling), s(4000, TierSibling)}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := 0
			if s := (Result{Servers: tt.servers}).Auto(); s != nil {
				got = s.Port
			}
			if got != tt.want {
				t.Errorf("Auto picked port %d, want %d", got, tt.want)
			}
		})
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
