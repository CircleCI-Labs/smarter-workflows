package policy

import (
	"reflect"
	"testing"
)

func envFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestMetaFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want map[string]any
	}{
		{
			name: "a branch build",
			env: map[string]string{
				"CIRCLE_PROJECT_ID":     "11111111-2222-3333-4444-555555555555",
				"CIRCLE_BUILD_NUM":      "17",
				"CIRCLE_BRANCH":         "feature/x",
				"CIRCLE_REPOSITORY_URL": "git@example.com:example-org/example-repo.git",
			},
			want: map[string]any{
				"project_id":   "11111111-2222-3333-4444-555555555555",
				"build_number": 17,
				"ssh_rerun":    false,
				"vcs": map[string]any{
					"branch":                "feature/x",
					"release_tag":           "",
					"origin_repository_url": "https://example.com/example-org/example-repo",
					"target_repository_url": "https://example.com/example-org/example-repo",
				},
			},
		},
		{
			name: "a tag build",
			env: map[string]string{
				"CIRCLE_TAG":            "v1.2.3",
				"CIRCLE_BUILD_NUM":      "3",
				"CIRCLE_REPOSITORY_URL": "https://example.com/example-org/example-repo.git",
			},
			want: map[string]any{
				"project_id":   "",
				"build_number": 3,
				"ssh_rerun":    false,
				"vcs": map[string]any{
					"branch":                "",
					"release_tag":           "v1.2.3",
					"origin_repository_url": "https://example.com/example-org/example-repo",
					"target_repository_url": "https://example.com/example-org/example-repo",
				},
			},
		},
		{
			name: "a pull request from a fork",
			env: map[string]string{
				"CIRCLE_BRANCH":         "pull/9",
				"CIRCLE_REPOSITORY_URL": "git@example.com:example-org/example-repo.git",
				"CIRCLE_PR_USERNAME":    "someone",
				"CIRCLE_PR_REPONAME":    "example-fork",
			},
			want: map[string]any{
				"project_id":   "",
				"build_number": 0,
				"ssh_rerun":    false,
				"vcs": map[string]any{
					"branch":                "pull/9",
					"release_tag":           "",
					"origin_repository_url": "https://example.com/someone/example-fork",
					"target_repository_url": "https://example.com/example-org/example-repo",
				},
			},
		},
		{
			name: "nothing is set",
			env:  map[string]string{},
			want: map[string]any{
				"project_id":   "",
				"build_number": 0,
				"ssh_rerun":    false,
				"vcs": map[string]any{
					"branch":                "",
					"release_tag":           "",
					"origin_repository_url": "",
					"target_repository_url": "",
				},
			},
		},
		{
			name: "a build number that is not a number",
			env:  map[string]string{"CIRCLE_BUILD_NUM": "abc"},
			want: map[string]any{
				"project_id":   "",
				"build_number": 0,
				"ssh_rerun":    false,
				"vcs": map[string]any{
					"branch":                "",
					"release_tag":           "",
					"origin_repository_url": "",
					"target_repository_url": "",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MetaFromEnv(envFrom(tt.env)); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("MetaFromEnv = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestMetaFromEnvReadsOnlyTheVariablesItNames(t *testing.T) {
	read := map[string]bool{}
	MetaFromEnv(func(name string) string {
		read[name] = true
		return ""
	})
	want := map[string]bool{
		"CIRCLE_PROJECT_ID": true, "CIRCLE_BUILD_NUM": true, "CIRCLE_BRANCH": true, "CIRCLE_TAG": true,
		"CIRCLE_REPOSITORY_URL": true, "CIRCLE_PR_USERNAME": true, "CIRCLE_PR_REPONAME": true,
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("read %v, want %v", read, want)
	}
}

func TestHTTPSRepoURL(t *testing.T) {
	tests := map[string]string{
		"":                                         "",
		"git@example.com:org/repo.git":             "https://example.com/org/repo",
		"git@example.com:org/repo":                 "https://example.com/org/repo",
		"ssh://git@example.com/org/repo.git":       "https://example.com/org/repo",
		"https://example.com/org/repo.git":         "https://example.com/org/repo",
		"https://example.com/org/repo/":            "https://example.com/org/repo",
		"https://example.com:8443/org/repo.git":    "https://example.com:8443/org/repo",
		"ssh://git@example.com:2222/org/repo.git":  "https://example.com/org/repo",
		"https://user:token@example.com/org/r.git": "https://example.com/org/r",
		"/local/path/repo":                         "/local/path/repo",
		"https://user:token@exa mple.com/org/r":    "",
		"https://":                                 "",
	}
	for raw, want := range tests {
		if got := httpsRepoURL(raw); got != want {
			t.Errorf("httpsRepoURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseMeta(t *testing.T) {
	wantMeta := map[string]any{
		"project_id": "abc",
		"vcs":        map[string]any{"branch": "main"},
		"list":       []any{"a", map[string]any{"b": "c"}},
	}
	good := map[string]string{
		"json":           `{"project_id": "abc", "vcs": {"branch": "main"}, "list": ["a", {"b": "c"}]}`,
		"json with tabs": "{\n\t\"project_id\": \"abc\",\n\t\"vcs\": {\"branch\": \"main\"},\n\t\"list\": [\"a\", {\"b\": \"c\"}]\n}",
		"yaml":           "project_id: abc\nvcs:\n  branch: main\nlist:\n  - a\n  - b: c\n",
	}
	for name, data := range good {
		t.Run(name, func(t *testing.T) {
			got, err := ParseMeta([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, wantMeta) {
				t.Fatalf("ParseMeta = %#v, want %#v", got, wantMeta)
			}
		})
	}

	t.Run("non-string keys", func(t *testing.T) {
		got, err := ParseMeta([]byte("vcs:\n  1: one\n  true: yes\n"))
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"vcs": map[string]any{"1": "one", "true": "yes"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseMeta = %#v, want %#v", got, want)
		}
	})

	for name, data := range map[string]string{
		"empty":    "",
		"a list":   "[1, 2]",
		"a string": `"x"`,
		"broken":   "{",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if _, err := ParseMeta([]byte(data)); err == nil {
				t.Fatal("ParseMeta succeeded, want an error")
			}
		})
	}
}
