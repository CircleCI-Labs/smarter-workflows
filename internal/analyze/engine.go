package analyze

import (
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// GoWalkFunc returns package directories from pure-Go import walking.
type GoWalkFunc func(repoRoot, pkgDir string) ([]string, error)

// Engine runs static inference for one repository checkout.
// maxGoWalkCalls caps Go import walks per map build (large monorepos).
const maxGoWalkCalls = 64

type Engine struct {
	RepoRoot       string
	GoWalk         GoWalkFunc
	goCache        map[string][]string
	goWalkCalls    int
	scriptFiles    int
	jobWorkspaceAt []string
}

// NewEngine builds an inference engine for repoRoot.
func NewEngine(repoRoot string) *Engine {
	return &Engine{
		RepoRoot: repoRoot,
		GoWalk:   defaultGoWalk,
		goCache:  map[string][]string{},
	}
}

func (e *Engine) resetPerJob() {
	e.scriptFiles = 0
	e.jobWorkspaceAt = nil
}

// InferJob collects inputs and the static step proof for one workflow entry.
func (e *Engine) InferJob(cfg *config.Config, entry *config.Entry) (*InputSet, *JobProof) {
	e.resetPerJob()
	into := newInputSet()
	if entry.Approval || entry.Kind == config.KindNoOp || entry.Kind == config.KindApproval {
		return into, &JobProof{}
	}
	proof := e.proveJob(cfg, entry, into)
	eng := e
	eng.InferConfigSignals(cfg, entry, into)
	return into, proof
}

// InferConfigSignals adds cache and artifact-only paths from raw YAML when present.
func normalizeAppDir(p string) string {
	p = strings.TrimPrefix(p, "~/project/")
	p = strings.TrimPrefix(p, "$CIRCLE_WORKING_DIRECTORY/")
	return p
}

func (e *Engine) InferConfigSignals(cfg *config.Config, entry *config.Entry, into *InputSet) {
	for _, rd := range cfg.CollectRunSteps(entry.Ref, entry.EntryArgs()) {
		for _, p := range rd.StoreArtifactPaths {
			_ = p // reports only per spec; not job inputs
		}
		for _, f := range rd.ChecksumFiles {
			into.addVerifiedFile(e.RepoRoot, ".", f, InputNote{Signal: SignalCacheChecksum, Detail: f})
		}
	}
}
