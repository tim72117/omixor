package model

import "testing"

func TestCommitRepoKeyKeepsProvider(t *testing.T) {
	for in, want := range map[string]string{
		"GitHub:Acme/App": "github:acme/app", " gitlab:Group/Sub/Proj ": "gitlab:group/sub/proj", "Acme/App": "acme/app",
	} {
		if got, ok := CommitRepoKey(in); !ok || got != want {
			t.Errorf("%q → %q %v,要 %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "github:", "gitlab:/", "a b", "//", ":"} {
		if k, ok := CommitRepoKey(in); ok {
			t.Errorf("%q 不該合格:%q", in, k)
		}
	}
	if p, path := CommitRepoSplit("github:a/b"); p != "github:" || path != "a/b" {
		t.Fatal(p, path)
	}
	if p, path := CommitRepoSplit("a/b"); p != "" || path != "a/b" {
		t.Fatal(p, path)
	}
	if RepoKey("github:Acme/App") != "acme/app" { // Issue.repo 的規則不變
		t.Fatal("RepoKey 不能改")
	}
}
