package config

import (
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// RunDetail is one `run` step the config shows, including steps inside local commands.
type RunDetail struct {
	Name               string
	Command            string
	WorkingDirectory   string
	ChecksumFiles      []string
	StoreArtifactPaths []string
	StoreTestPaths     []string
}

var checksumRe = regexp.MustCompile(`\{\{\s*checksum\s+"([^"]+)"\s*\}\}`)

// CollectRunSteps returns every run step of jobRef, including local commands and
// pre-steps or post-steps on the workflow entry when entryArgs is set.
func (c *Config) CollectRunSteps(jobRef string, entryArgs *yaml.Node) []RunDetail {
	def := c.jobDef(jobRef)
	if def == nil {
		return nil
	}
	sc := c.rootScope()
	var out []RunDetail
	if entryArgs != nil {
		for _, key := range []string{"pre-steps", "post-steps"} {
			if seq := lookup(entryArgs, key); seq != nil && seq.Kind == yaml.SequenceNode {
				out = append(out, sc.scanSteps(seq, map[string]bool{}, 0)...)
			}
		}
	}
	if steps := lookup(def, "steps"); steps != nil {
		out = append(out, sc.scanSteps(steps, map[string]bool{}, 0)...)
	}
	return out
}

// CollectConfigSignals reads working_directory, checkout path, and orb-style
// parameters from the job definition and workflow entry args.
func (c *Config) CollectConfigSignals(jobRef string, entryArgs *yaml.Node) (checkoutPath, workingDir string, params map[string]string) {
	params = map[string]string{}
	checkoutPath = c.JobCheckoutPath(jobRef)
	def := c.jobDef(jobRef)
	if def != nil {
		if wd, ok := scalarValue(lookup(def, "working_directory")); ok {
			workingDir = wd
		}
	}
	if entryArgs != nil && entryArgs.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(entryArgs.Content); i += 2 {
			k, v := entryArgs.Content[i], entryArgs.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			name := k.Value
			switch name {
			case "pre-steps", "post-steps", "requires", "filters", "matrix", "context", "type", "name", "serial-group":
				continue
			}
			if s, ok := scalarValue(v); ok && s != "" && !strings.Contains(s, "<<") {
				params[name] = s
			}
		}
	}
	return checkoutPath, workingDir, params
}

func (s scope) scanSteps(steps *yaml.Node, seen map[string]bool, depth int) []RunDetail {
	if steps == nil || steps.Kind != yaml.SequenceNode || depth > maxDepth {
		return nil
	}
	var out []RunDetail
	for _, st := range steps.Content {
		if rd, ok := runDetailOf(st); ok {
			out = append(out, rd)
		}
		scanCachesAndStores(st, &out)
		if cmd, isCmd := stepCommandName(st); isCmd {
			if body := s.command(cmd); body != nil && !seen[cmd] {
				seen[cmd] = true
				if sub := lookup(body, "steps"); sub != nil {
					out = append(out, s.scanSteps(sub, seen, depth+1)...)
				}
			}
		}
	}
	return out
}

func runDetailOf(st *yaml.Node) (RunDetail, bool) {
	if st.Kind != yaml.MappingNode || len(st.Content) < 2 {
		return RunDetail{}, false
	}
	if st.Content[0].Value != "run" {
		return RunDetail{}, false
	}
	body := st.Content[1]
	rd := RunDetail{}
	switch body.Kind {
	case yaml.ScalarNode:
		rd.Command = body.Value
	case yaml.MappingNode:
		if name, ok := scalarValue(lookup(body, "name")); ok {
			rd.Name = name
		}
		if wd, ok := scalarValue(lookup(body, "working_directory")); ok {
			rd.WorkingDirectory = wd
		}
		if cmd, ok := scalarValue(lookup(body, "command")); ok {
			rd.Command = cmd
		}
	default:
		return RunDetail{}, false
	}
	if rd.Command == "" {
		return RunDetail{}, false
	}
	return rd, true
}

func scanCachesAndStores(st *yaml.Node, out *[]RunDetail) {
	if st.Kind != yaml.MappingNode || len(st.Content) < 2 {
		return
	}
	key := st.Content[0].Value
	body := st.Content[1]
	switch key {
	case "save_cache":
		if body.Kind == yaml.MappingNode {
			if k, ok := scalarValue(lookup(body, "key")); ok {
				for _, m := range checksumRe.FindAllStringSubmatch(k, -1) {
					if len(m) > 1 {
						appendChecksum(out, m[1])
					}
				}
			}
		}
	case "store_artifacts", "store_test_results":
		if body.Kind == yaml.MappingNode {
			if p, ok := scalarValue(lookup(body, "path")); ok && p != "" {
				rd := RunDetail{}
				if key == "store_artifacts" {
					rd.StoreArtifactPaths = []string{p}
				} else {
					rd.StoreTestPaths = []string{p}
				}
				*out = append(*out, rd)
			}
		}
	}
}

func appendChecksum(out *[]RunDetail, file string) {
	if len(*out) == 0 {
		*out = append(*out, RunDetail{ChecksumFiles: []string{file}})
		return
	}
	last := &(*out)[len(*out)-1]
	last.ChecksumFiles = append(last.ChecksumFiles, file)
}

// OutlineStep is one job step in order for static proof.
type OutlineStep struct {
	Num            int
	Key            string
	Command        string
	WorkingDir     string
	ChecksumFiles  []string
	StoreArtifacts []string
	StoreTests     []string
}

// CollectOutlineSteps lists every step of jobRef in execution order.
func (c *Config) CollectOutlineSteps(jobRef string, entryArgs *yaml.Node) []OutlineStep {
	def := c.jobDef(jobRef)
	sc := c.rootScope()
	var out []OutlineStep
	n := 0
	if entryArgs != nil {
		for _, key := range []string{"pre-steps", "post-steps"} {
			if seq := lookup(entryArgs, key); seq != nil {
				out = append(out, sc.outlineSteps(seq, map[string]bool{}, 0, &n)...)
			}
		}
	}
	if def != nil {
		if steps := lookup(def, "steps"); steps != nil {
			out = append(out, sc.outlineSteps(steps, map[string]bool{}, 0, &n)...)
		}
	}
	return out
}

func (s scope) outlineSteps(steps *yaml.Node, seen map[string]bool, depth int, n *int) []OutlineStep {
	if steps == nil || steps.Kind != yaml.SequenceNode || depth > maxDepth {
		return nil
	}
	var out []OutlineStep
	for _, st := range steps.Content {
		if st.Kind == yaml.MappingNode && len(st.Content) >= 2 && st.Content[0].Kind == yaml.ScalarNode {
			key := st.Content[0].Value
			body := st.Content[1]
			switch key {
			case "run":
				rd, ok := runDetailOf(st)
				if ok {
					*n++
					out = append(out, OutlineStep{Num: *n, Key: key, Command: rd.Command, WorkingDir: rd.WorkingDirectory})
				}
			case "when", "unless":
				if sub := lookup(body, "steps"); sub != nil {
					out = append(out, s.outlineSteps(sub, seen, depth+1, n)...)
				}
			default:
				if cmd, isCmd := stepCommandName(st); isCmd {
					if body := s.command(cmd); body != nil && !seen[cmd] {
						seen[cmd] = true
						if sub := lookup(body, "steps"); sub != nil {
							out = append(out, s.outlineSteps(sub, seen, depth+1, n)...)
							continue
						}
					}
				}
				var scratch []RunDetail
				scanCachesAndStores(st, &scratch)
				rd := RunDetail{}
				if len(scratch) > 0 {
					rd = scratch[0]
				}
				*n++
				out = append(out, OutlineStep{
					Num:            *n,
					Key:            key,
					ChecksumFiles:  rd.ChecksumFiles,
					StoreArtifacts: rd.StoreArtifactPaths,
					StoreTests:     rd.StoreTestPaths,
				})
			}
		}
		if cmd, isCmd := stepCommandName(st); isCmd {
			if body := s.command(cmd); body != nil && !seen[cmd] {
				seen[cmd] = true
				if sub := lookup(body, "steps"); sub != nil {
					out = append(out, s.outlineSteps(sub, seen, depth+1, n)...)
				}
			}
		}
	}
	return out
}
