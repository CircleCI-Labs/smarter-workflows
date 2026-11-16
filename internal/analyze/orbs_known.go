package analyze

import (
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

type orbHandler func(repoRoot, baseWD string, params map[string]string, into *InputSet) (understood bool, summary string)

var knownOrbs = map[string]orbHandler{
	"node/run":     orbNodeAppDir,
	"node/test":    orbNodeAppDir,
	"node/install": orbNodeAppDir,
}

func proveOrbStep(repoRoot, orbKey, baseWD string, params map[string]string, into *InputSet) (bool, string) {
	h, ok := knownOrbs[orbKey]
	if !ok {
		return false, ""
	}
	u, sum := h(repoRoot, baseWD, params, into)
	return u, sum
}

func orbNodeAppDir(repoRoot, baseWD string, params map[string]string, into *InputSet) (bool, string) {
	app := params["app-dir"]
	if app == "" {
		app = params["app_dir"]
	}
	if app == "" {
		return false, ""
	}
	app = normalizeAppDir(app)
	dir := app
	if !strings.Contains(app, "/") {
		dir = resolveWD(repoRoot, baseWD, app)
	}
	into.addVerifiedDir(repoRoot, ".", dir, InputNote{Signal: SignalOrbParam, Detail: "app-dir"})
	return true, "orb node app-dir " + dir
}

func entryOrbKey(ref string) (string, bool) {
	if strings.Contains(ref, "/") {
		return ref, true
	}
	return "", false
}

func paramsFromEntry(cfg *config.Config, entry *config.Entry) map[string]string {
	_, _, params := cfg.CollectConfigSignals(entry.Ref, entry.EntryArgs())
	return params
}
