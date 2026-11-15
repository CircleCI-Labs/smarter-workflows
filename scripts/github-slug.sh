#!/bin/sh
# github-slug.sh: resolve the GitHub owner/repo for gh and publish scripts.
#
# On GitHub App projects, CIRCLE_PROJECT_USERNAME is the CircleCI org name, not
# the GitHub owner. Prefer GITHUB_REPOSITORY (owner/repo). Otherwise parse
# CIRCLE_REPOSITORY_URL (https or git@ forms, with or without .git).
#
# Sourced, not executed:
#   github_slug_from_env          prints owner/repo
#   github_https_remote_from_env  prints https://host/owner/repo.git
_github_slug_err() {
  printf 'github-slug: %s\n' "$*" >&2
  return 1
}

# slug_from_clone_url URL: print owner/repo from a GitHub clone URL.
slug_from_clone_url() {
  url=$1
  case $url in
    https://*/*)
      rest=${url#https://}
      path=${rest#*/}
      path=${path%.git}
      path=${path%/}
      case $path in
        */*) printf '%s\n' "$path"; return 0 ;;
      esac
      ;;
    git@*:*)
      path=${url#git@*:}
      path=${path%.git}
      case $path in
        */*) printf '%s\n' "$path"; return 0 ;;
      esac
      ;;
  esac
  return 1
}

# github_slug_from_env: owner/repo for this checkout.
github_slug_from_env() {
  if [ -n "${GITHUB_REPOSITORY:-}" ]; then
    case $GITHUB_REPOSITORY in
      */*) printf '%s\n' "$GITHUB_REPOSITORY"; return 0 ;;
    esac
    _github_slug_err "GITHUB_REPOSITORY must be owner/repo, not $GITHUB_REPOSITORY"
    return 1
  fi
  url=${CIRCLE_REPOSITORY_URL:-}
  if [ -n "$url" ]; then
    slug=$(slug_from_clone_url "$url") || {
      _github_slug_err "cannot parse CIRCLE_REPOSITORY_URL as a GitHub clone URL"
      return 1
    }
    printf '%s\n' "$slug"
    return 0
  fi
  _github_slug_err "set GITHUB_REPOSITORY or CIRCLE_REPOSITORY_URL to find the GitHub repository"
  return 1
}

# github_https_remote_from_env: https clone URL for git push (publish).
github_https_remote_from_env() {
  if [ -n "${PUBLISH_REMOTE:-}" ]; then
    printf '%s\n' "$PUBLISH_REMOTE"
    return 0
  fi
  if [ -n "${GITHUB_REPOSITORY:-}" ]; then
    printf 'https://github.com/%s.git\n' "$GITHUB_REPOSITORY"
    return 0
  fi
  url=${CIRCLE_REPOSITORY_URL:-}
  [ -n "$url" ] || {
    _github_slug_err "set GITHUB_REPOSITORY, CIRCLE_REPOSITORY_URL, or PUBLISH_REMOTE"
    return 1
  }
  case $url in
    https://*)
      url=${url%.git}
      printf '%s.git\n' "$url"
      ;;
    git@*:*)
      host=${url#git@}
      host=${host%%:*}
      slug=$(slug_from_clone_url "$url") || {
        _github_slug_err "cannot parse CIRCLE_REPOSITORY_URL"
        return 1
      }
      printf 'https://%s/%s.git\n' "$host" "$slug"
      ;;
    *)
      _github_slug_err "cannot turn CIRCLE_REPOSITORY_URL into an https remote"
      return 1
      ;;
  esac
}