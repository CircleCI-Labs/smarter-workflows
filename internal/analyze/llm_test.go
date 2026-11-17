package analyze

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalLLMPromptTinyBudget(t *testing.T) {
	root := filepath.Join(testingDir(t), "testdata", "repo")
	pkg, err := BuildLLMPrompt(
		root,
		filepath.Join(testingDir(t), "testdata", "minimal.yml"),
		"ci",
	)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a large synthetic file so the budget must truncate file_contents.
	pkg.FileContents["internal/sample/big.txt"] = FileContent{
		Content: strings.Repeat("x", 8_000),
	}
	const tiny = 3_500
	raw, err := MarshalLLMPrompt(pkg, tiny)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > tiny {
		t.Fatalf("encoded length %d exceeds budget %d", len(raw), tiny)
	}
	var got LLMPromptPackage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(raw[:min(500, len(raw))]))
	}
	if got.Instructions == "" {
		t.Fatal("missing instructions")
	}
	if got.OutputSchema == nil || len(got.Jobs) == 0 || len(got.RepoTree) == 0 {
		t.Fatalf("missing core fields: schema=%v jobs=%d tree=%d",
			got.OutputSchema != nil, len(got.Jobs), len(got.RepoTree))
	}
	seenTrunc := false
	for p, fc := range got.FileContents {
		if fc.Truncated {
			seenTrunc = true
		}
		if fc.Content == "" && !fc.Truncated {
			t.Fatalf("empty content for %s", p)
		}
	}
	if len(got.FileContents) > 0 && !seenTrunc && len(raw) >= tiny-100 {
		t.Log("budget filled without per-file truncation (small repo)")
	}
}

func TestValidateLLMPromptPackage(t *testing.T) {
	root := filepath.Clean(filepath.Join(testingDir(t), "..", ".."))
	dir := t.TempDir()
	if err := EmitLLMPrompt(
		root,
		filepath.Join(root, "examples/demo/.circleci/continue.yml"),
		"demo",
		dir,
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLLMPromptFile(filepath.Join(dir, "prompt.json")); err != nil {
		t.Fatal(err)
	}
}
