package config_test

import (
	"os"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

func TestCollectRunSteps(t *testing.T) {
	const yml = `
version: 2.1
commands:
  helper:
    steps:
      - run:
          command: scripts/helper.sh
jobs:
  j:
    steps:
      - checkout
      - helper
      - run:
          name: cache
          command: echo ok
workflows:
  w:
    jobs:
      - j
`
	cfg, err := config.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	wf, err := cfg.Workflow("w")
	if err != nil {
		t.Fatal(err)
	}
	e := wf.Entries[0]
	runs := cfg.CollectRunSteps(e.Ref, nil)
	if len(runs) < 2 {
		t.Fatalf("runs %d", len(runs))
	}
	found := map[string]bool{}
	for _, r := range runs {
		found[r.Command] = true
	}
	for _, want := range []string{"scripts/helper.sh", "echo ok"} {
		if !found[want] {
			t.Fatalf("missing %q in %#v", want, runs)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
