package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MetaFromEnv builds data.meta from the environment of a CircleCI job, in the
// shape the policy service uses:
//
//	{
//	  "project_id":   "<uuid>",                         // CIRCLE_PROJECT_ID
//	  "build_number": 17,                               // CIRCLE_BUILD_NUM
//	  "ssh_rerun":    false,
//	  "vcs": {
//	    "branch":                "main",                // CIRCLE_BRANCH
//	    "release_tag":           "v1.2.3",              // CIRCLE_TAG
//	    "origin_repository_url": "https://host/o/r",    // see below
//	    "target_repository_url": "https://host/o/r"     // CIRCLE_REPOSITORY_URL
//	  }
//	}
//
// The repository URL is CIRCLE_REPOSITORY_URL in https form. In a pull request
// from a fork, the origin URL points to the fork (CIRCLE_PR_USERNAME and
// CIRCLE_PR_REPONAME). Values that are not set are empty strings, or 0 for the
// build number. getenv is os.Getenv in production. MetaFromEnv reads only the
// variables named here.
func MetaFromEnv(getenv func(string) string) map[string]any {
	buildNumber, _ := strconv.Atoi(strings.TrimSpace(getenv("CIRCLE_BUILD_NUM")))

	target := httpsRepoURL(getenv("CIRCLE_REPOSITORY_URL"))
	origin := target
	if owner, repo := getenv("CIRCLE_PR_USERNAME"), getenv("CIRCLE_PR_REPONAME"); owner != "" && repo != "" {
		if u, err := url.Parse(target); err == nil && u.Host != "" {
			u.Path = "/" + owner + "/" + repo
			origin = u.String()
		}
	}

	return map[string]any{
		"project_id":   getenv("CIRCLE_PROJECT_ID"),
		"build_number": buildNumber,
		"ssh_rerun":    false,
		"vcs": map[string]any{
			"branch":                getenv("CIRCLE_BRANCH"),
			"release_tag":           getenv("CIRCLE_TAG"),
			"origin_repository_url": origin,
			"target_repository_url": target,
		},
	}
}

// httpsRepoURL turns a clone URL (git@host:owner/repo.git, ssh://git@host/...
// or https://host/owner/repo.git) into https://host/owner/repo. Credentials in
// the URL are dropped, and so is a URL that cannot be parsed. Anything that
// does not look like a URL, such as a local path, is returned as it is.
func httpsRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "" // not worth the risk of passing on credentials
		}
		// The port of an ssh URL is not the port of the web site.
		host, path = u.Hostname(), u.Path
		if u.Scheme == "http" || u.Scheme == "https" {
			host = u.Host
		}
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		// scp-like: git@host:owner/repo.git
		rest := raw[strings.Index(raw, "@")+1:]
		host, path, _ = strings.Cut(rest, ":")
	default:
		return raw
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return "https://" + host + "/" + path
}

// ParseMeta reads data.meta from a JSON object, or from YAML.
func ParseMeta(data []byte) (map[string]any, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		v = nil
		if yerr := yaml.Unmarshal(data, &v); yerr != nil {
			return nil, fmt.Errorf("meta is not valid JSON: %w", err)
		}
	}
	m, ok := jsonTypes(v).(map[string]any)
	if !ok {
		return nil, errors.New("meta must be a JSON object")
	}
	return m, nil
}

// jsonTypes converts the maps a YAML decoder may return with non-string keys
// into maps with string keys, which encoding/json can marshal.
func jsonTypes(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, e := range v {
			v[k] = jsonTypes(e)
		}
		return v
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[fmt.Sprint(k)] = jsonTypes(e)
		}
		return out
	case []any:
		for i, e := range v {
			v[i] = jsonTypes(e)
		}
		return v
	default:
		return v
	}
}
