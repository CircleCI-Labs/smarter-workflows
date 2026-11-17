package analyze

import (
	"fmt"
	"path"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

func (e *Engine) proveJob(cfg *config.Config, entry *config.Entry, into *InputSet) *JobProof {
	proof := &JobProof{}
	if entry.Approval || entry.Kind == config.KindNoOp || entry.Kind == config.KindApproval {
		return proof
	}
	if orb, ok := entryOrbKey(entry.Ref); ok {
		checkout, jobWD, params := cfg.CollectConfigSignals(entry.Ref, entry.EntryArgs())
		baseWD := jobWorkingDir(checkout, jobWD)
		for k, v := range paramsFromEntry(cfg, entry) {
			params[k] = v
		}
		u, sum := proveOrbStep(e.RepoRoot, orb, baseWD, params, into)
		rec := StepRecord{Num: 1, Key: orb, Understood: u, Summary: sum}
		if !u {
			rec.Opaque = orb
			proof.Steps = append(proof.Steps, rec)
			proof.failStep(1, fmt.Sprintf("orb %q", orb))
			return proof
		}
		proof.Steps = append(proof.Steps, rec)
		return proof
	}
	jd := cfg.JobDef(entry.Ref)
	if !jd.Known {
		proof.failStep(0, fmt.Sprintf("job %q", entry.Ref))
		return proof
	}
	checkout, jobWD, params := cfg.CollectConfigSignals(entry.Ref, entry.EntryArgs())
	baseWD := jobWorkingDir(checkout, jobWD)
	for k, v := range params {
		switch k {
		case "app-dir", "path", "working_directory":
			if v != "" {
				v = normalizeAppDir(v)
				into.addVerifiedDir(e.RepoRoot, baseWD, v, InputNote{Signal: SignalOrbParam, Detail: k})
			}
		}
	}
	e.jobWorkspaceAt = normalizeWorkspaceRoots(cfg.CollectAttachWorkspaceAt(entry.Ref, entry.EntryArgs()), baseWD)
	steps := cfg.CollectOutlineSteps(entry.Ref, entry.EntryArgs())
	for _, st := range steps {
		rec := StepRecord{Num: st.Num, Key: st.Key}
		wd := baseWD
		if st.WorkingDir != "" {
			wd = resolveWD(e.RepoRoot, baseWD, st.WorkingDir)
		}
		if _, ok := understoodStepKeys[st.Key]; ok {
			rec.Understood = true
			rec.Summary = st.Key
			for _, f := range st.ChecksumFiles {
				into.addVerifiedFile(e.RepoRoot, wd, f, InputNote{Signal: SignalCacheChecksum, Detail: f})
			}
			proof.Steps = append(proof.Steps, rec)
			continue
		}
		if st.Key == "run" {
			if op := e.analyzeRunCommand(e.RepoRoot, wd, st.Command, into, proof); op != "" {
				rec.Understood = false
				rec.Opaque = op
				proof.Steps = append(proof.Steps, rec)
				proof.failStep(st.Num, op)
				return proof
			}
			rec.Understood = true
			rec.Summary = truncateOpaque(st.Command)
			proof.Steps = append(proof.Steps, rec)
			continue
		}
		if strings.Contains(st.Key, "/") {
			u, sum := proveOrbStep(e.RepoRoot, st.Key, baseWD, params, into)
			rec.Understood = u
			rec.Summary = sum
			if !u {
				rec.Opaque = st.Key
				proof.Steps = append(proof.Steps, rec)
				proof.failStep(st.Num, fmt.Sprintf("orb %q", st.Key))
				return proof
			}
			proof.Steps = append(proof.Steps, rec)
			continue
		}
		rec.Understood = false
		rec.Opaque = st.Key
		proof.Steps = append(proof.Steps, rec)
		proof.failStep(st.Num, st.Key)
		return proof
	}
	if proof.requiresRepoInputs && !into.hasInputs() {
		proof.failNoInputs()
	}
	return proof
}

func jobWorkingDir(checkout, jobWD string) string {
	baseWD := "."
	if checkout != "" {
		baseWD = checkout
	}
	if jobWD != "" {
		baseWD = path.Clean(path.Join(baseWD, jobWD))
	}
	return baseWD
}
